package main

import (
	"context"
	"crypto/sha256"
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
	"github.com/avvvet/oxygen/pkg/wallet"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
	net "github.com/otwocoin/otwo-blockchain/internal/pkg/net"
	util "github.com/otwocoin/otwo-blockchain/internal/pkg/util"
	"go.uber.org/zap"
)

var (
	logger, _ = zap.NewProduction() // or NewProduction, or NewDevelopment
)

type addrList []multiaddr.Multiaddr

type Config struct {
	Port           int
	ProtocolID     string
	Rendezvous     string
	Seed           int64
	DiscoveryPeers addrList
}

func main() {
	config := Config{}

	flag.StringVar(&config.Rendezvous, "meet", "otwo", "peer joining place")
	flag.Int64Var(&config.Seed, "seed", 0, "0 is for random PeerID")
	flag.Var(&config.DiscoveryPeers, "peer", "Perr address for peer discovery")
	flag.StringVar(&config.ProtocolID, "protocolid", "/p2p/otwo", "")
	flag.IntVar(&config.Port, "port", 0, "port for peer")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())

	h, err := net.NewHost(ctx, config.Seed, config.Port)
	if err != nil {
		log.Fatal(err)
	}

	for _, addr := range h.Addrs() {
		log.Printf("  %s/p2p/%s", addr, h.ID().Pretty())
	}

	/*create gossipSub */
	pubSubInst, err := net.InitPubSub(ctx, h, "otwo")
	if err != err {
		logger.Sugar().Fatal("Error: creating pubsub", err)
	}

	dht, err := net.NewDHT(ctx, h, config.DiscoveryPeers)
	if err != nil {
		log.Fatal(err)
	}

	go net.Discover(ctx, h, dht, config.Rendezvous)
	go ListenBroadcast(ctx, pubSubInst.Subscription, h)
	go ListPeers(h)

	Process(ctx, pubSubInst)
	run(h, cancel)
}

func Process(ctx context.Context, ps *net.PubsubInst) {
	// Example: this will give us a 32 byte output
	randomString, err := util.GenerateRandomString(32)
	if err != nil {
		// Serve an appropriately vague error to the
		// user, but log the details internally.
		panic(err)
	}

	//temp create wallet address and sign the first genesis transaction output
	adamWallet := wallet.NewWallet()
	eveWallet := wallet.NewWallet()

	senderPK := util.Encode(adamWallet.PublicKey)
	receiverPK := util.Encode(eveWallet.PublicKey)

	natureRawTx := &wallet.RawTx{
		SenderPublicKey:       senderPK,
		SenderOxygenAddress:   adamWallet.OxygenAddress,
		SenderRandomHash:      sha256.Sum256([]byte(randomString)),
		ReceiverPublicKey:     senderPK,
		ReceiverOxygenAddress: adamWallet.OxygenAddress,
		Token:                 900,
	}

	txout := &blockchain.TxOutput{
		RawTx:     natureRawTx,
		Signature: natureRawTx.Sign(adamWallet.PrivateKey),
	}

	chain, err := blockchain.InitChain(txout)
	if err != nil {
		fmt.Print(err)
	}
	defer chain.Ledger.Db.Close()

	rawTx1 := &wallet.RawTx{
		SenderPublicKey:       senderPK,
		SenderOxygenAddress:   adamWallet.OxygenAddress,
		SenderRandomHash:      sha256.Sum256([]byte(randomString)),
		Token:                 400,
		ReceiverPublicKey:     receiverPK,
		ReceiverOxygenAddress: eveWallet.OxygenAddress,
	}

	txout1 := &blockchain.TxOutput{
		RawTx:     rawTx1,
		Signature: rawTx1.Sign(adamWallet.PrivateKey),
	}
	tx1 := chain.NewTransaction(txout1)
	chain.ChainBlock(`data {} `+strconv.Itoa(1), []*blockchain.Transaction{tx1})

	rawTx2 := &wallet.RawTx{
		SenderPublicKey:       senderPK,
		SenderOxygenAddress:   adamWallet.OxygenAddress,
		SenderRandomHash:      sha256.Sum256([]byte(randomString)),
		Token:                 490,
		ReceiverPublicKey:     receiverPK,
		ReceiverOxygenAddress: eveWallet.OxygenAddress,
	}

	txout2 := &blockchain.TxOutput{
		RawTx:     rawTx2,
		Signature: rawTx2.Sign(adamWallet.PrivateKey),
	}
	tx2 := chain.NewTransaction(txout2)

	//broadcast
	newblock, err := chain.ChainBlock(`data {} `+strconv.Itoa(1), []*blockchain.Transaction{tx2})
	if err != nil {
		panic(err)
	}

	data, err := json.Marshal(&net.BroadcastData{Type: "NEWBLOCK", Data: newblock})
	if err != nil {
		panic(err)
	}
	go Broadcaster(ctx, ps.Topic, data)

	var i = 0
	for {
		data, err := chain.Ledger.Get([]byte(strconv.Itoa(i)))
		if err != nil {
			break
		} else {
			block := &blockchain.Block{}
			err = json.Unmarshal(data, block)
			if err != nil {
				logger.Sugar().Fatal("unable to get block from store")
			}

			fmt.Printf("############## BlockHeight %v ############# \n", i)
			fmt.Printf("Timestamp : %s \n", time.Unix(block.Timestamp, 0).Format(time.RFC3339))
			fmt.Printf("Block Hash: %x\n", block.Hash)
			fmt.Printf("Data: %s\n", block.Data)
			fmt.Printf("Transaction ID: %x Inputs %+v  Outputs %+v\n", block.Transaction[0].ID, block.Transaction[0].Inputs, block.Transaction[0].Outputs)
			fmt.Printf("Merkle root: %x\n", block.MerkleRoot)
			fmt.Printf("Previous Hash: %x\n", block.PrevHash)
			fmt.Printf("Difficulty: %v\n", block.Difficulty)
			fmt.Printf("Nonce: %v\n", block.Nonce)
			fmt.Printf("BlockHeight: %v\n", block.BlockHeight)
		}
		i++
	}

}

type Stream struct {
	Type  string
	Block *blockchain.Block
}

func ListenBroadcast(ctx context.Context, s *pubsub.Subscription, h host.Host) {
	for {
		msg, err := s.Next(ctx)
		if err != nil {
			fmt.Println("errrrrrrrr", err)
		}

		//only consider messages delivered by other peers
		if msg.ReceivedFrom == h.ID() {
			continue
		}

		var b *net.BroadcastData
		json.Unmarshal([]byte(msg.Data), &b)

		switch t := b.Type; t {
		case "NEWBLOCK":
			ValidateBlock(b.Data)
			fmt.Println("Newwwwwwwwwwwwww arrived ******************************* ", msg.ReceivedFrom)
		}

	}
}

func ValidateBlock(b []byte) {
	block := &blockchain.Block{}
	err := json.Unmarshal(b, block)
	if err != nil {
		panic(err)
	}
    
	block.
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

func ListPeers(h host.Host) {
	ticker := time.NewTicker(time.Second * 5)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p := h.Network().Peers()
			for _, b := range p {
				fmt.Printf("%s \n", b)
			}
		}
	}

}

func run(h host.Host, cancel func()) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	<-c
	fmt.Printf("\rExiting... \n")

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
