package mdutils

import (
	"testing"

	"github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/blocks/blockstore"
	bsrv "github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/blockservice"
	"github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/exchange/offline"
	dag "github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/ipfs/go-ipfs/merkledag"
	ds "github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-datastore"
	dssync "github.com/heems/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-datastore/sync"
)

func Mock(t testing.TB) dag.DAGService {
	bstore := blockstore.NewBlockstore(dssync.MutexWrap(ds.NewMapDatastore()))
	bserv, err := bsrv.New(bstore, offline.Exchange(bstore))
	if err != nil {
		t.Fatal(err)
	}
	return dag.NewDAGService(bserv)
}
