package filetransfer

import (
	"bufio"
	"context"
	"crypto"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	lcrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	ListProtocol = "/sukun/file/list"
	GetProtocol  = "/sukun/file/fetch"
	peerChanSize = 100
)

type Node struct {
	privKey lcrypto.PrivKey
	host    host.Host
	dht     *dht.IpfsDHT
	ctx     context.Context
	cancel  func()

	dirPath string
}

type Config struct {
	privKey lcrypto.PrivKey
	pubKey  lcrypto.PubKey
	port    string
}

type Option func(c *Config) error

func WithPrivKey(pk crypto.PrivateKey) Option {
	return func(c *Config) error {
		var err error
		c.privKey, c.pubKey, err = lcrypto.KeyPairFromStdKey(pk)
		return err
	}
}

func WithPort(p string) Option {
	return func(c *Config) error {
		c.port = p
		return nil
	}
}

func NewNode(dirPath string, opts ...Option) (*Node, error) {

	cfg := Config{
		port: "9700",
	}
	for _, o := range opts {
		if err := o(&cfg); err != nil {
			return nil, err
		}
	}

	peerChan := make(chan peer.AddrInfo, 1)
	lOpts := []libp2p.Option{
		libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/udp/%s/quic-v1", cfg.port)),
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
		libp2p.EnableHolePunching(),
	}

	if cfg.privKey != nil {
		lOpts = append(lOpts, libp2p.Identity(cfg.privKey))
	}

	h, err := libp2p.New(lOpts...)
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

	go func() {
		defer func() {
			if err := recover(); err != nil {
				log.Print(err)
			}
		}()
		http.Handle("/metrics", promhttp.Handler())
		http.ListenAndServe("0.0.0.0:9600", nil)
	}()

	n := &Node{
		host:    h,
		dht:     d,
		ctx:     ctx,
		cancel:  cancel,
		dirPath: dirPath,
	}
	n.host.SetStreamHandler(ListProtocol, n.handleFileList)
	n.host.SetStreamHandler(GetProtocol, n.sendFile)

	go trackWebTransport(n)
	return n, err
}

func (n *Node) Addrs() []ma.Multiaddr {
	return n.host.Addrs()
}

func (n *Node) NumPeers() int {
	return len(n.host.Network().Peers())
}

func (n *Node) ID() peer.ID {
	return n.host.ID()
}

func (n *Node) Close() {
	n.cancel()
	n.host.Close()
	n.dht.Close()
}

func (n *Node) FileNames() ([]string, error) {
	entries, err := os.ReadDir(n.dirPath)
	if err != nil {
		return nil, fmt.Errorf("read dir failed: %w", err)
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

func (n *Node) handleFileList(s network.Stream) {
	f, err := n.FileNames()
	if err != nil {
		s.Reset()
		return
	}
	b, err := json.Marshal(f)
	if err != nil {
		s.Reset()
		return
	}
	if _, err := s.Write(b); err != nil {
		s.Reset()
		return
	}
	s.Close()
}

func (n *Node) GetFileList(ai peer.AddrInfo) []string {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx = network.WithUseTransient(ctx, "fetch file list")

	n.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.TempAddrTTL)
	s, err := n.host.NewStream(ctx, ai.ID, ListProtocol)
	if err != nil {
		log.Printf("%s open stream failed %s: %s\n", ListProtocol, ai, err)
		return nil
	}
	var ss []string
	if err := json.NewDecoder(s).Decode(&ss); err != nil {
		log.Printf("error reading file list %s: %s\n", ai, err)
	}
	return ss
}

func (n *Node) sendFile(s network.Stream) {
	defer s.Close()

	if s.Stat().Transient {
		log.Println("cannot send file over transient connection", s.Conn().RemotePeer())
		s.Reset()
		return
	}

	var name string
	if err := json.NewDecoder(s).Decode(&name); err != nil {
		log.Println("invalid file name: ", err)
		s.Reset()
		return
	}

	f, err := os.Open(path.Join(n.dirPath, name))
	if err != nil {
		log.Println("couldn't open file", name, err)
		s.Reset()
		return
	}

	_, err = io.Copy(s, bufio.NewReader(f))
	if err != nil {
		s.Reset()
		return
	}
}

func (n *Node) GetFile(ai peer.AddrInfo, file string) []byte {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.host.Peerstore().AddAddrs(ai.ID, ai.Addrs, peerstore.TempAddrTTL)
	s, err := n.host.NewStream(ctx, ai.ID, GetProtocol)
	if err != nil {
		log.Println(err)
		return nil
	}

	b, err := json.Marshal(file)
	if err != nil {
		s.Reset()
		return nil
	}
	b = append(b, byte('\n'))
	if _, err := s.Write(b); err != nil {
		s.Reset()
		return nil
	}

	res, err := io.ReadAll(s)
	if err != nil {
		return nil
	}
	return res
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
			select {
			case peerChan <- peer.AddrInfo{ID: evt.Peer}:
			default:
			}
		case <-ctx.Done():
			return
		}
	}
}

func trackWebTransport(n *Node) {
	sub, err := n.host.EventBus().Subscribe(new(event.EvtPeerIdentificationCompleted))
	if err != nil {
		panic(err)
	}
	ch := make(chan bool, 10)
	checked := make(map[peer.ID]bool)
	for {
		select {
		case evt := <-sub.Out():
			e := evt.(event.EvtPeerIdentificationCompleted)
			p := e.Peer
			addrs := n.host.Peerstore().Addrs(p)
			var wAddr, qAddr ma.Multiaddr
			for _, a := range addrs {
				if !manet.IsPublicAddr(a) {
					continue
				}
				if _, err := a.ValueForProtocol(ma.P_WEBTRANSPORT); err == nil {
					wAddr = a
					continue
				}
				if _, err := a.ValueForProtocol(ma.P_QUIC_V1); err == nil {
					qAddr = a
					continue
				}
			}
			if wAddr == nil || qAddr == nil {
				av, _ := n.host.Peerstore().Get(p, "AgentVersion")
				if strings.Contains(av.(string), "0.22") {
					fmt.Println("no WT", p, addrs)
				}
				continue
			}

			if checked[e.Peer] {
				continue
			}
			checked[e.Peer] = true

			select {
			case ch <- true:
			default:
				fmt.Println("dropping")
				continue
			}
			go func(p peer.ID) {
				defer func() { <-ch }()
				sb := strings.Builder{}
				av, _ := n.host.Peerstore().Get(p, "AgentVersion")
				sb.WriteString(fmt.Sprintln("dialing: ", p, av))
				n.host.Network().ClosePeer(p)
				n.host.Peerstore().ClearAddrs(p)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := n.host.Connect(ctx, peer.AddrInfo{ID: p, Addrs: []ma.Multiaddr{wAddr}})
				cancel()
				sb.WriteString(fmt.Sprintln("dial complete: waddr", wAddr, err == nil))

				n.host.Network().ClosePeer(p)
				n.host.Peerstore().ClearAddrs(p)
				ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
				err = n.host.Connect(ctx, peer.AddrInfo{ID: p, Addrs: []ma.Multiaddr{qAddr}})
				cancel()
				sb.WriteString(fmt.Sprintln("dial complete: qaddr", qAddr, err == nil))
				fmt.Println(sb.String())
			}(e.Peer)
		case <-n.ctx.Done():
			return
		}
	}
}
