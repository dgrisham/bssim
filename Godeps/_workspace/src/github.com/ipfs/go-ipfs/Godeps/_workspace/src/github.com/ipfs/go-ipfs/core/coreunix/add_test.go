package coreunix

import (
	"os"
	"path"
	"testing"

	"github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/core"
	"github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/repo"
	"github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/repo/config"
	"github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/util/testutil"
	"github.com/heems/go-ipfs/Godeps/_workspace/src/golang.org/x/net/context"
)

func TestAddRecursive(t *testing.T) {
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	r := &repo.Mock{
		C: config.Config{
			Identity: config.Identity{
				PeerID: "Qmfoo", // required by offline node
			},
		},
		D: testutil.ThreadSafeCloserMapDatastore(),
	}
	node, err := core.NewIPFSNode(context.Background(), core.Offline(r))
	if err != nil {
		t.Fatal(err)
	}
	if k, err := AddR(node, path.Join(here, "test_data")); err != nil {
		t.Fatal(err)
	} else if k != "QmWCCga8AbTyfAQ7pTnGT6JgmRMAB3Qp8ZmTEFi5q5o8jC" {
		t.Fatal("keys do not match")
	}
}
