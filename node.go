package filetransfer

import (
	"context"
	"log"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

const peerChanSize = 100

type Node struct {
	host   host.Host
	dht    *dht.IpfsDHT
	ctx    context.Context
	cancel func()
}

func NewNode() (*Node, error) {
	peerChan := make(chan peer.AddrInfo, 100)
	h, err := libp2p.New(
		libp2p.ListenAddrStrings("/ip4/0.0.0.0/udp/9786/quic-v1"),
		libp2p.EnableAutoRelayWithPeerSource(
			func(ctx context.Context, num int) <-chan peer.AddrInfo {
				ch := make(chan peer.AddrInfo, num)

				go func() {
					ctxDone := false
					for i := 0; i < num; i++ {
						select {
						case ai := <-peerChan:
							ch <- ai
						case <-ctx.Done():
							ctxDone = true
						}
						if ctxDone {
							break
						}
					}
					close(ch)
				}()
				return ch
			},
		),
	)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	go relayProvider{host: h}.Provide(ctx, peerChan)

	d, err := dht.New(ctx, h, dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...))
	if err != nil {
		cancel()
		return nil, err
	}
	if err := d.Bootstrap(ctx); err != nil {
		cancel()
		return nil, err
	}

	return &Node{
		host:   h,
		dht:    d,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

func (n *Node) Addrs() []ma.Multiaddr {
	return n.host.Addrs()
}

func (n *Node) NumPeers() int {
	return len(n.host.Network().Peers())
}

type relayProvider struct {
	host host.Host
}

func (r relayProvider) Provide(ctx context.Context, peerChan chan peer.AddrInfo) {
	sub, err := r.host.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted))
	if err != nil {
		log.Printf("subscription failed: %s", err)
		return
	}
	for {
		select {
		case e, ok := <-sub.Out():
			if !ok {
				return
			}
			evt := e.(event.EvtPeerIdentificationCompleted)
			peerChan <- peer.AddrInfo{ID: evt.Peer}
		case <-ctx.Done():
			return
		}
	}
}
