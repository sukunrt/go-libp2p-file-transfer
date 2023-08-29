package main

import (
	"bufio"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base32"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	filetransfer "github.com/sukun/go-libp2p-file-transfer"
)

func main() {
	privKey, err := getPrivKey(".key")
	if err != nil {
		log.Print(err)
		return
	}
	dirPath := flag.String("dir", "data", "directory to serve")
	port := flag.String("port", "9700", "port for the server")
	newKey := flag.Bool("newKey", false, "should use new key for ID")
	flag.Parse()

	opts := []filetransfer.Option{filetransfer.WithPort(*port)}
	if !*newKey {
		opts = append(opts, filetransfer.WithPrivKey(privKey))
	}
	n, err := filetransfer.NewNode(*dirPath, opts...)
	if err != nil {
		log.Print(err)
		return
	}
OUTER:
	for {
		r := bufio.NewScanner(os.Stdin)
		for r.Scan() {
			line := r.Text()
			words := strings.Split(line, " ")
			switch strings.ToLower(words[0]) {
			case "list":
				id, err := peer.Decode(words[1])
				if err != nil {
					fmt.Println(err)
					continue OUTER
				}
				addr := ma.StringCast(words[2])
				fmt.Println(n.GetFileList(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{addr}}))
			case "get":
				id, err := peer.Decode(words[2])
				if err != nil {
					fmt.Println(err)
					continue OUTER
				}
				addr := ma.StringCast(words[3])
				fmt.Println(string(n.GetFile(peer.AddrInfo{ID: id, Addrs: []ma.Multiaddr{addr}}, words[1])))
			case "addrs":
				fmt.Println(n.ID(), n.Addrs())
			}
		}
	}
}

func getPrivKey(path string) (crypto.PrivateKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return newPrivKey(path)
	}
	defer f.Close()

	bk, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	pkb, err := base32.StdEncoding.DecodeString(string(bk))
	if err != nil {
		return nil, err
	}
	pk := ed25519.PrivateKey(pkb)
	return &pk, nil
}

func newPrivKey(path string) (crypto.PrivateKey, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	_, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(base32.StdEncoding.EncodeToString(privKey))
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	return privKey, nil
}
