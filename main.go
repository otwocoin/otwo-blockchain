package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/avvvet/oxygen/pkg/blockchain"
	net "github.com/avvvet/oxygen/pkg/net"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	"github.com/otwocoin/otwo-blockchain/internal/app"
	"go.uber.org/zap"
)

var (
	network            = "otwo"
	chain_ledger_path  = "./ledger/chain"
	wallet_ledger_path = "./ledger/wallet"
	logger, _          = zap.NewProduction() // or NewProduction, or NewDevelopment
)

type addrList []multiaddr.Multiaddr

type Config struct {
	HttpPort       uint
	PeerPort       uint
	ProtocolID     string
	Rendezvous     string
	Seed           int64
	DiscoveryPeers addrList
}

func main() {
	app.Art()
	config := Config{}

	flag.StringVar(&config.Rendezvous, "meet", "otwo", "peer joining place")
	flag.Int64Var(&config.Seed, "seed", 0, "0 is for random PeerID")
	flag.Var(&config.DiscoveryPeers, "peer", "Perr address for peer discovery")
	flag.StringVar(&config.ProtocolID, "protocolid", "/p2p/otwo", "")
	flag.UintVar(&config.HttpPort, "httpPort", 0, "http port for otwo wallet")
	flag.UintVar(&config.PeerPort, "peerPort", 0, "port for otwo blockchain peer that connects to the network")
	flag.Parse()

	app.NewDir(chain_ledger_path)
	app.NewDir(wallet_ledger_path)

	/*wallet address for this node */
	wl, err := app.InitWalletLedger(wallet_ledger_path)
	if err != nil {
		logger.Sugar().Warn("critical error in wallet address")
	}

	ctx, cancel := context.WithCancel(context.Background())

	h, err := net.NewHost(ctx, config.Seed, int(config.PeerPort))
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("peer network address")
	for _, addr := range h.Addrs() {
		log.Printf("  %s/p2p/%s", addr, h.ID().Pretty())
	}
	fmt.Println("")

	/*create gossipSub */
	ps, err := net.InitPubSub(ctx, h, network)
	if err != err {
		logger.Sugar().Fatal("Error: creating pubsub", err)
	}

	dht, err := net.NewDHT(ctx, h, config.DiscoveryPeers)
	if err != nil {
		log.Fatal(err)
	}

	go net.Discover(ctx, h, dht, config.Rendezvous)

	/* init chain ledger */
	chain, err := blockchain.InitChainLedger(chain_ledger_path)
	if err != nil {
		fmt.Print(err)
	}
	defer chain.Ledger.Db.Close()

	/*
	  http server
	  pass ctx, Topic and wallet list
	*/
	http := app.NewApp(ctx, config.HttpPort, ps.Topic, wl, chain)
	go http.Run()

	go ListenBroadcast(ctx, ps, h, chain)

	run(h, cancel)
}

type Stream struct {
	Type  string
	Block *blockchain.Block
}

func ListenBroadcast(ctx context.Context, psi *net.PubsubInst, h host.Host, chain *blockchain.Chain) {
	for {
		msg, err := psi.Subscription.Next(ctx)
		if err != nil {
			break //when context cancel called from main
		}

		//only consider messages delivered by other peers
		// if msg.ReceivedFrom == h.ID() {
		// 	continue
		// }

		var b *net.BroadcastData
		json.Unmarshal([]byte(msg.Data), &b)

		switch t := b.Type; t {
		case "NEWBLOCK":
			//			ValidateBlock(b.Data, chain)
			fmt.Println("Newwwwwwwwwwwwww arrived ******************************* ", msg.ReceivedFrom)
		case "NEWTXOUTPUT":
			err := Mine(ctx, b.Data, chain, psi.Topic)
			if err != nil {
				logger.Sugar().Warn(err)
			}
		}

	}
}
func Mine(ctx context.Context, b []byte, chain *blockchain.Chain, topic *pubsub.Topic) error {
	txoutput := &blockchain.TxOutput{}
	err := json.Unmarshal(b, txoutput)
	if err != nil {
		logger.Sugar().Warn("error: transaction decoding error")
		return err
	}

	/*create the transaction from raw*/
	tx, err := chain.NewTransaction(txoutput)
	if err != nil {
		logger.Sugar().Warn("mine error: could not create transaction")
		return err
	}

	/*create block*/
	block, err := chain.ChainBlock(`data {} `+strconv.Itoa(1), []*blockchain.Transaction{tx})
	if err != nil {
		logger.Sugar().Warn("mine error: could not chain block")
		return err
	}

	//encode the new block
	blockByte, err := json.Marshal(block)
	if err != nil {
		logger.Sugar().Warn("mine error: could not encode the newblock for broadcast")
		return err
	}

	data, err := json.Marshal(&net.BroadcastData{Type: "NEWBLOCK", Data: blockByte})
	if err != nil {
		logger.Sugar().Warn("mine error: could not encode broadcast data ")
		return err
	}

	err = topic.Publish(ctx, data)
	if err != nil {
		logger.Sugar().Warn("mine error: could not broadcast")
		return err
	}

	logger.Sugar().Info("💥️ new block broadcasted")
	return nil
}

func ValidateBlock(b []byte, chain *blockchain.Chain) {
	block := &blockchain.Block{}
	err := json.Unmarshal(b, block)
	if err != nil {
		panic(err)
	}

	//check if the blockheight
	if (block.BlockHeight - chain.LastBlock.BlockHeight) == 1 {
		if block.IsBlockValid(block.Nonce) && block.PrevHash == chain.LastBlock.Hash {
			err = chain.Ledger.Upsert([]byte(strconv.Itoa(block.BlockHeight)), b)
			if err != nil {
				logger.Sugar().Fatal("unable to store data")
			}

			logger.Sugar().Info("chain updated from network  ********************************* ", block.BlockHeight)
		}
	}
}

func Broadcaster(ctx context.Context, t *pubsub.Topic, data []byte) {
	ticker := time.NewTicker(time.Second * 15)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			t.Publish(ctx, data)
		}

	}

}

// func ListPeers(h host.Host) {
// 	ticker := time.NewTicker(time.Second * 5)
// 	defer ticker.Stop()

// 	for {
// 		select {
// 		case <-ticker.C:
// 			p := h.Network().Peers()
// 			for _, b := range p {
// 				fmt.Printf("%s \n", b)
// 			}
// 		}
// 	}

// }

func run(h host.Host, cancel func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	<-c
	fmt.Printf("\r👋️ stopped...\n")

	cancel()

	if err := h.Close(); err != nil {
		panic(err)
	}
	os.Exit(0)
}

func (al *addrList) String() string {
	strs := make([]string, len(*al))
	for i, addr := range *al {
		strs[i] = addr.String()
	}
	return strings.Join(strs, ",")
}

func (al *addrList) Set(value string) error {
	addr, err := multiaddr.NewMultiaddr(value)
	if err != nil {
		return err
	}
	*al = append(*al, addr)
	return nil
}
