package filetransfer

import (
	"os"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"golang.org/x/exp/slices"
)

func TestNodeListFiles(t *testing.T) {
	os.Mkdir("ft-test", os.FileMode(0777))
	_, err := os.Create("ft-test/a")
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Create("ft-test/b")
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Create("ft-test/c")
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := os.RemoveAll("ft-test"); err != nil {
			t.Fatal(err)
		}
	}()

	n, err := NewNode("ft-test", WithPort("0"))
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()

	cli, err := NewNode("ft-test", WithPort("0"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	files := cli.GetFileList(peer.AddrInfo{ID: n.ID(), Addrs: n.Addrs()})

	if !slices.Contains(files, "a") || !slices.Contains(files, "b") || !slices.Contains(files, "c") {
		t.Fatalf("invalid files list %s", files)
	}
}

func TestNodeGetFiles(t *testing.T) {
	os.Mkdir("ft-test", os.FileMode(0777))
	f, err := os.Create("ft-test/a")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("hello world!"))
	f.Sync()
	f.Close()

	defer func() {
		if err := os.RemoveAll("ft-test"); err != nil {
			t.Fatal(err)
		}
	}()

	n, err := NewNode("ft-test", WithPort("0"))
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()

	cli, err := NewNode("ft-test", WithPort("0"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	file := cli.GetFile(peer.AddrInfo{ID: n.ID(), Addrs: n.Addrs()}, "a")
	if string(file) != "hello world!" {
		t.Errorf("invalid file content %s", string(file))
	}
}
