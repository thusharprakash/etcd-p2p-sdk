package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
)

const (
	serviceTag = "p2p-sync-service"
	etcdDir    = "default.etcd"
)

type discoveryNotifee struct {
	peerChan chan peer.AddrInfo
}

func (n *discoveryNotifee) HandlePeerFound(pi peer.AddrInfo) {
	n.peerChan <- pi
}

func setupMDNS(ctx context.Context, h host.Host, peerChan chan peer.AddrInfo) error {
	service := mdns.NewMdnsService(h, serviceTag, &discoveryNotifee{peerChan: peerChan})
	return service.Start()
}

func startEmbeddedEtcd() (*embed.Etcd, error) {
	cfg := embed.NewConfig()
	cfg.Dir = etcdDir
	cfg.ListenClientUrls = []url.URL{{Scheme: "http", Host: "localhost:2379"}}
	cfg.ListenPeerUrls = []url.URL{{Scheme: "http", Host: "localhost:2380"}}
	cfg.InitialCluster = "default=http://localhost:2380"
	cfg.ClusterState = embed.ClusterStateFlagNew

	e, err := embed.StartEtcd(cfg)
	if err != nil {
		return nil, err
	}

	select {
	case <-e.Server.ReadyNotify():
		log.Println("Server is ready!")
	case <-time.After(60 * time.Second):
		e.Server.Stop() // trigger a shutdown
		log.Println("Server took too long to start!")
	}

	return e, nil
}

func main() {
	ctx := context.Background()

	// Start the embedded etcd server
	etcdServer, err := startEmbeddedEtcd()
	if err != nil {
		log.Fatalf("Failed to start embedded etcd: %v", err)
	}
	defer etcdServer.Close()

	// Create a new libp2p Host
	h, err := libp2p.New()
	if err != nil {
		log.Fatalf("Failed to create libp2p host: %v", err)
	}
	defer h.Close()

	// Channel to receive peer information
	peerChan := make(chan peer.AddrInfo)

	// Setup mDNS discovery
	if err := setupMDNS(ctx, h, peerChan); err != nil {
		log.Fatalf("Failed to setup mDNS: %v", err)
	}

	go func() {
		for pi := range peerChan {
			fmt.Printf("Discovered new peer: %s\n", pi.ID.String())
			// Connect to the new peer
			if err := h.Connect(ctx, pi); err != nil {
				fmt.Printf("Failed to connect to peer: %v\n", err)
			} else {
				fmt.Printf("Connected to peer: %s\n", pi.ID.String())
			}
		}
	}()

	// Setup etcd client
	etcdClient, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"localhost:2379"},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatalf("Failed to connect to etcd: %v", err)
	}
	defer etcdClient.Close()

	// Simulate event creation and synchronization
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		for {
			<-ticker.C
			event := fmt.Sprintf("Event at %v", time.Now())
			fmt.Printf("Creating new event: %s\n", event)

			// Store event in etcd
			_, err := etcdClient.Put(ctx, "/events/latest", event)
			if err != nil {
				log.Printf("Failed to store event in etcd: %v", err)
			}

			// Retrieve the latest event from etcd to ensure synchronization
			resp, err := etcdClient.Get(ctx, "/events/latest")
			if err != nil {
				log.Printf("Failed to retrieve event from etcd: %v", err)
			} else {
				for _, kv := range resp.Kvs {
					fmt.Printf("Latest event from etcd: %s\n", kv.Value)
				}
			}
		}
	}()

	// Keep the main function running
	select {}
}
