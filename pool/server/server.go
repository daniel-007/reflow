// Copyright 2017 GRAIL, Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Package server exposes a pool implementation for remote access.
package server

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/grailbio/base/digest"
	"github.com/grailbio/reflow"
	"github.com/grailbio/reflow/errors"
	"github.com/grailbio/reflow/pool"
	repositoryserver "github.com/grailbio/reflow/repository/server"
	"github.com/grailbio/reflow/rest"
)

// NewNode returns a rest.Node that implements the pool REST API.
func NewNode(p pool.Pool) rest.Node {
	v1 := rest.Mux{
		"allocs": allocsNode{p},
		"offers": offersNode{p},
	}
	return rest.Mux{"v1": v1}
}

type offersNode struct {
	p pool.Pool
}

func (n offersNode) Walk(ctx context.Context, call *rest.Call, path string) rest.Node {
	offer, err := n.p.Offer(ctx, path)
	if err != nil {
		call.Error(err)
		return nil
	}
	return rest.DoFunc(func(ctx context.Context, call *rest.Call) {
		if !call.Allow("GET", "POST") {
			return
		}
		switch call.Method() {
		case "GET":
			json := pool.OfferJSON{
				ID:        offer.ID(),
				Available: offer.Available(),
			}
			call.Reply(http.StatusOK, json)
		case "POST":
			var meta pool.AllocMeta
			if call.Unmarshal(&meta) != nil {
				return
			}
			alloc, err := offer.Accept(ctx, meta)
			if err != nil {
				call.Error(err)
				return
			}
			call.Reply(http.StatusOK, pool.AllocInspect{
				ID:        alloc.ID(),
				Resources: alloc.Resources(),
			})
		}
	})
}

func (n offersNode) Do(ctx context.Context, call *rest.Call) {
	if !call.Allow("GET") {
		return
	}
	offers, err := n.p.Offers(ctx)
	if err != nil {
		call.Error(err)
		return
	}
	jsons := make([]pool.OfferJSON, len(offers))
	for i, offer := range offers {
		jsons[i].ID = offer.ID()
		jsons[i].Available = offer.Available()
	}
	call.Reply(http.StatusOK, jsons)
}

type allocsNode struct {
	m pool.Pool
}

func (n allocsNode) Walk(ctx context.Context, call *rest.Call, path string) rest.Node {
	alloc, err := n.m.Alloc(ctx, path)
	if err != nil {
		call.Error(err)
		return nil
	}
	return allocNode{alloc}
}

func (n allocsNode) Do(ctx context.Context, call *rest.Call) {
	if !call.Allow("GET") {
		return
	}
	allocs, err := n.m.Allocs(ctx)
	if err != nil {
		call.Error(err)
		return
	}
	jsons := make([]pool.AllocInspect, len(allocs))
	for i, alloc := range allocs {
		jsons[i] = pool.AllocInspect{
			ID:        alloc.ID(),
			Resources: alloc.Resources(),
		}
	}
	call.Reply(http.StatusOK, jsons)
}

type allocNode struct {
	a pool.Alloc
}

func (n allocNode) Walk(ctx context.Context, call *rest.Call, path string) rest.Node {
	switch path {
	case "keepalive":
		return rest.DoFunc(func(ctx context.Context, call *rest.Call) {
			if !call.Allow("POST") {
				return
			}
			var arg struct {
				Interval time.Duration
			}
			if call.Unmarshal(&arg) != nil {
				return
			}
			d, err := n.a.Keepalive(ctx, arg.Interval)
			if err != nil {
				call.Error(err)
				return
			}
			call.Reply(http.StatusOK, struct{ Interval time.Duration }{d})
		})
	case "execs":
		return execsNode{n.a}
	case "repository":
		repo := n.a.Repository()
		if repo == nil {
			return nil
		}
		return repositoryserver.Node{repo}
	default:
		return nil
	}
}

func (n allocNode) Do(ctx context.Context, call *rest.Call) {
	if !call.Allow("DELETE", "GET") {
		return
	}
	switch call.Method() {
	case "GET":
		inspect, err := n.a.Inspect(ctx)
		if err != nil {
			call.Error(err)
			return
		}
		call.Reply(http.StatusOK, inspect)
	case "DELETE":
		err := n.a.Free(ctx)
		if err != nil {
			call.Error(err)
			return
		}
		call.Reply(http.StatusOK, "alloc freed")
	}
}

type execsNode struct{ a pool.Alloc }

func (n execsNode) Walk(ctx context.Context, call *rest.Call, path string) rest.Node {
	id, err := reflow.Digester.Parse(path)
	if err != nil {
		call.Error(errors.E("walk", path, err))
		return nil
	}
	switch call.Method() {
	case "PUT":
		// TODO: validate exec ID
		return putExecNode{n.a, id}
	default:
		o, err := n.a.Get(context.TODO(), id)
		if err != nil {
			call.Error(err)
			return nil
		}
		return execNode{o}
	}
}

func (n execsNode) Do(ctx context.Context, call *rest.Call) {
	if !call.Allow("GET") {
		return
	}
	execs, err := n.a.Execs(ctx)
	if err != nil {
		call.Error(err)
		return
	}
	list := make([]digest.Digest, len(execs))
	for i := range execs {
		list[i] = execs[i].ID()
	}
	call.Reply(http.StatusOK, list)
}

type putExecNode struct {
	e  reflow.Executor
	id digest.Digest
}

func (n putExecNode) Walk(ctx context.Context, call *rest.Call, path string) rest.Node {
	return nil
}

func (n putExecNode) Do(ctx context.Context, call *rest.Call) {
	if !call.Allow("PUT") {
		return
	}
	var cfg reflow.ExecConfig
	if call.Unmarshal(&cfg) != nil {
		return
	}
	if _, err := n.e.Put(ctx, n.id, cfg); err != nil {
		call.Error(err)
	} else {
		call.Replyf(http.StatusOK, "exec %s created", n.id)
	}
}

type execNode struct {
	e reflow.Exec
}

func (n execNode) logNode(stdout, stderr bool, follow string) rest.Node {
	f := false
	if follow == "true" {
		f = true
	}
	return rest.DoFunc(func(ctx context.Context, call *rest.Call) {
		if !call.Allow("GET") {
			return
		}
		rc, err := n.e.Logs(ctx, stdout, stderr, f)
		if err != nil {
			call.Error(err)
			return
		}
		_, err = io.Copy(&rest.StreamingCall{call}, rc)

		if err != nil {
			call.Error(err)
			return
		}
		rc.Close()
		call.Write(http.StatusOK, bytes.NewReader(nil))
	})
}

func (n execNode) shellNode() rest.Node {
	return rest.DoFunc(func(ctx context.Context, call *rest.Call) {
		if !call.Allow("POST") {
			return
		}
		rwc, err := n.e.Shell(ctx)
		if err != nil {
			call.Error(err)
			return
		}
		go func() {
			io.Copy(rwc, call.Body())
		}()

		_, err = io.Copy(&rest.StreamingCall{call}, rwc)
		if err != nil {
			call.Error(err)
			return
		}
		rwc.Close()
		call.Write(http.StatusOK, bytes.NewReader(nil))
	})
}

func (n execNode) Walk(ctx context.Context, call *rest.Call, path string) rest.Node {
	u, err := url.Parse(path)
	if err != nil {
		call.Error(err)
		return nil
	}
	switch u.Path {
	default:
		return nil
	case "wait":
		return rest.DoFunc(func(ctx context.Context, call *rest.Call) {
			if !call.Allow("GET") {
				return
			}
			if err := n.e.Wait(ctx); err != nil {
				call.Error(err)
			} else {
				call.Reply(http.StatusOK, "exec ready")
			}
		})
	case "logs":
		return n.logNode(true, true, u.Query().Get("follow"))
	case "stderr":
		return n.logNode(false, true, u.Query().Get("follow"))
	case "stdout":
		return n.logNode(true, false, u.Query().Get("follow"))
	case "shell":
		return n.shellNode()
	case "result":
		return rest.DoFunc(func(ctx context.Context, call *rest.Call) {
			if !call.Allow("GET") {
				return
			}
			r, err := n.e.Result(ctx)
			if err != nil {
				call.Error(err)
				return
			}
			call.Reply(http.StatusOK, r)
		})
	case "promote":
		return rest.DoFunc(func(ctx context.Context, call *rest.Call) {
			if !call.Allow("POST") {
				return
			}
			if err := n.e.Promote(ctx); err != nil {
				call.Error(err)
				return
			}
			call.Reply(http.StatusOK, "exec promoted")
		})
	}
}

func (n execNode) Do(ctx context.Context, call *rest.Call) {
	if !call.Allow("GET", "HEAD") {
		return
	}
	inspect, err := n.e.Inspect(ctx)
	if err != nil {
		call.Error(err)
		return
	}
	call.Reply(http.StatusOK, inspect)
}
