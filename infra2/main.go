package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"time"

	etcdcl "github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/embed"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
	"net/http"
	"os/signal"
)

const (
	defaultStartTimeout          = 900 * time.Second
	defaultStartRejoinTimeout    = 60 * time.Second
	defaultMemberCleanerInterval = 15 * time.Second
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), defaultStartRejoinTimeout)
	defer cancel()

	// c1, _ := url.Parse("http://127.0.0.1:2379")
	p1, _ := url.Parse("http://127.0.0.1:2380")
	// m1, _ := url.Parse("http://127.0.0.1:2381")

	c2, _ := url.Parse("http://127.0.0.2:2379")
	p2, _ := url.Parse("http://127.0.0.2:2380")
	m2, _ := url.Parse("http://127.0.0.2:2381")

	// Configure the server.
	etcdCfg := embed.NewConfig()
	etcdCfg.ClusterState = embed.ClusterStateFlagExisting
	etcdCfg.Name = "infra2"
	etcdCfg.Dir = "/tmp/infra2"
	etcdCfg.PeerAutoTLS = false
	// etcdCfg.PeerTLSInfo = c.cfg.PeerSC.TLSInfo()
	etcdCfg.ClientAutoTLS = false
	// etcdCfg.ClientTLSInfo = c.cfg.ClientSC.TLSInfo()
	etcdCfg.InitialCluster = fmt.Sprintf("infra1=%s,infra2=%s", p1, p2)
	etcdCfg.LPUrls = []url.URL{*p2}
	etcdCfg.APUrls = []url.URL{*p2}
	etcdCfg.LCUrls = []url.URL{*c2}
	etcdCfg.ACUrls = []url.URL{*c2}
	etcdCfg.ListenMetricsUrls = []url.URL{*m2}
	etcdCfg.Metrics = "extensive"
	etcdCfg.QuotaBackendBytes = 2147483648

	// Start the server.
	server, err := embed.StartEtcd(etcdCfg)

	// Discard the gRPC logs, as the embed server will set that regardless of what was set before (i.e. at startup).
	etcdcl.SetLogger(grpclog.NewLoggerV2(ioutil.Discard, ioutil.Discard, os.Stderr))

	if err != nil {
		log.Fatalln(fmt.Errorf("failed to start etcd: %s", err))
	}
	// Wait until the server announces its ready, or until the start timeout is exceeded.
	//
	// When the server is joining an existing Client, it won't be until it has received a snapshot from healthy
	// members and sync'd from there.
	select {
	case <-server.Server.ReadyNotify():
		break
	case <-server.Err():
		// FIXME.
		panic("server failed to start, and continuing might stale the application, exiting instead (github.com/coreos/etcd/issues/9533)")
		Stop(server, false, false)
		log.Fatalln(fmt.Errorf("server failed to start: %s", err))
	case <-ctx.Done():
		// FIXME.
		panic("server failed to start, and continuing might stale the application, exiting instead (github.com/coreos/etcd/issues/9533)")
		Stop(server, false, false)
		log.Fatalln(fmt.Errorf("server took too long to become ready"))
	}

	go runErrorWatcher(server)

	select {}
}

func runErrorWatcher(server *embed.Etcd) {
	select {
	case <-server.Server.StopNotify():
		log.Warnf("etcd server is stopping")
		// c.isRunning = false
		return
	case <-server.Err():
		log.Warnf("etcd server has crashed")
		Stop(server, false, false)
	}
}

func Stop(server *embed.Etcd, graceful, snapshot bool) {
	if !graceful {
		server.Server.HardStop()
		server.Server = nil
	}
	server.Close()
	return
}

func disco() {
	var srv http.Server

	idleConnsClosed := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint

		// We received an interrupt signal, shut down.
		if err := srv.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("HTTP server Shutdown: %v", err)
		}
		close(idleConnsClosed)
	}()


	mux := http.NewServeMux()
	// mux.Handle("/api/", apiHandler{})
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		// The "/" pattern matches everything, so we need to check
		// that we're at the root here.
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		fmt.Fprintf(w, "Welcome to the home page!")
	})
	srv.Handler = mux


	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		// Error starting or closing listener:
		log.Printf("HTTP server ListenAndServe: %v", err)
	}

	<-idleConnsClosed
}
