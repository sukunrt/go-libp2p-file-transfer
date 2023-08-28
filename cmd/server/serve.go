package main

import (
	"fmt"
	"log"
	"time"

	filetransfer "github.com/sukun/go-libp2p-file-transfer"
)

func main() {
	n, err := filetransfer.NewNode()
	if err != nil {
		log.Print(err)
		return
	}
	for i := 0; i < 100; i++ {
		fmt.Println("peers:", n.NumPeers(), "our addrs", n.Addrs())
		time.Sleep(1 * time.Second)
	}
}
