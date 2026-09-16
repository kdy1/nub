package fetch

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nubjs/nub/pm-go/internal/lockfile"
	"github.com/nubjs/nub/pm-go/internal/store"
)

func TestBatchImportsLocalsBeforeRegistryAndStreamsFetchedIndexes(t *testing.T) {
	f, p, _ := registryFixture(t, "shared batch contents")
	packages := map[string]*lockfile.Package{}
	for _, name := range []string{"a", "b", "c"} {
		q := p.Clone()
		q.Name = name
		q.DepPath = name + "@1.2.3"
		packages[q.DepPath] = q
	}
	local := lockfile.NewPackage("local", "0.0.0")
	local.Source = &lockfile.Source{Kind: lockfile.Link, Path: "local"}
	packages[local.DepPath] = local
	var localImported atomic.Bool
	load := func(ctx context.Context, p *lockfile.Package) (Result, error) {
		if p.Source != nil {
			localImported.Store(true)
			return Result{}, nil
		}
		if !localImported.Load() {
			return Result{}, errors.New("registry started before local sources")
		}
		return f.Registry(ctx, p, false)
	}
	emitted := 0
	batch, err := Packages(t.Context(), packages, 2, load, func(_ context.Context, _ string, index store.PackageIndex) error { emitted++; return nil })
	if err != nil || len(batch.Indices) != 3 || batch.Cached != 2 || batch.Fetched != 1 || emitted != 1 {
		t.Fatal(batch, emitted, err)
	}
	for _, index := range batch.Indices {
		assertContent(t, Result{Index: index}, "shared batch contents")
	}
}

func TestBatchFailureAndConsumerFailureCancelAndJoinWorkers(t *testing.T) {
	for _, consumerFailure := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		started := make(chan struct{})
		var active atomic.Int32
		failure := errors.New("fixture failure")
		load := func(ctx context.Context, p *lockfile.Package) (Result, error) {
			active.Add(1)
			defer active.Add(-1)
			if p.Name == "a" {
				select {
				case <-started:
				case <-ctx.Done():
					return Result{}, ctx.Err()
				}
				if consumerFailure {
					return Result{Index: store.PackageIndex{}}, nil
				}
				return Result{}, failure
			}
			close(started)
			<-ctx.Done()
			return Result{}, ctx.Err()
		}
		packages := map[string]*lockfile.Package{"a": lockfile.NewPackage("a", "1"), "b": lockfile.NewPackage("b", "1")}
		_, err := Packages(ctx, packages, 2, load, func(context.Context, string, store.PackageIndex) error { return failure })
		if !errors.Is(err, failure) || active.Load() != 0 {
			t.Fatal("workers survived failure", active.Load(), err)
		}
	}
}

func TestContextualizeSourceIndexesAndComputedPins(t *testing.T) {
	g := lockfile.NewGraph()
	p := lockfile.NewPackage("git-pkg", "1.0.0")
	p.DepPath = "git-pkg@git+12345678(peer@2.0.0)"
	p.Source = &lockfile.Source{Kind: lockfile.Git}
	g.Packages[p.DepPath] = p
	index := store.PackageIndex{"index.js": {Hash: "source-hash"}}
	got := ContextualizeIndices(map[string]store.PackageIndex{"git-pkg@git+12345678": index, "git-pkg@1.0.0": {"wrong": {}}}, g)
	if !reflect.DeepEqual(got[p.DepPath], index) {
		t.Fatal(got)
	}
	delete(got[p.DepPath], "index.js")
	if len(index) != 1 {
		t.Fatal("remapping aliased source index map")
	}
	p = lockfile.NewPackage("p", "1.0.0")
	p.DepPath += "(peer@2.0.0)"
	batch, err := Packages(t.Context(), map[string]*lockfile.Package{p.DepPath: p}, 1, func(context.Context, *lockfile.Package) (Result, error) {
		return Result{Index: store.PackageIndex{}, ComputedIntegrity: new("pin")}, nil
	}, nil)
	if err != nil || batch.ComputedIntegrities["p@1.0.0"] != "pin" {
		t.Fatal(batch, err)
	}
}
