package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/multiformats/go-multiaddr"
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

	flag.StringVar(&config.Rendezvous, "meet", "oxygen", "peer joining place")
	flag.Int64Var(&config.Seed, "seed", 0, "0 is for random PeerID")
	flag.Var(&config.DiscoveryPeers, "peer", "Perr address for peer discovery")
	flag.StringVar(&config.ProtocolID, "protocolid", "/p2p/oxygen", "")
	flag.IntVar(&config.Port, "port", 0, "port for peer")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())

	h, err := NewHost(ctx, config.Seed, config.Port)
	if err != nil {
		log.Fatal(err)
	}

	for _, addr := range h.Addrs() {
		log.Printf("  %s/p2p/%s", addr, h.ID().Pretty())
	}

	dht, err := NewDHT(ctx, h, config.DiscoveryPeers)
	if err != nil {
		log.Fatal(err)
	}

	go Discover(ctx, h, dht, config.Rendezvous)
	go ListPeers(h)
	run(h, cancel)
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
