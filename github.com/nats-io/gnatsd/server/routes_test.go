// Copyright 2013-2016 Apcera Inc. All rights reserved.

package server

import (
	"fmt"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/nats-io/nats"
)

func TestRouteConfig(t *testing.T) {
	opts, err := ProcessConfigFile("./configs/cluster.conf")
	if err != nil {
		t.Fatalf("Received an error reading route config file: %v\n", err)
	}

	golden := &Options{
		Host:               "apcera.me",
		Port:               4242,
		Username:           "derek",
		Password:           "bella",
		AuthTimeout:        1.0,
		ClusterHost:        "127.0.0.1",
		ClusterPort:        4244,
		ClusterUsername:    "route_user",
		ClusterPassword:    "top_secret",
		ClusterAuthTimeout: 1.0,
		LogFile:            "/tmp/nats_cluster_test.log",
		PidFile:            "/tmp/nats_cluster_test.pid",
	}

	// Setup URLs
	r1, _ := url.Parse("nats-route://foo:bar@apcera.me:4245")
	r2, _ := url.Parse("nats-route://foo:bar@apcera.me:4246")

	golden.Routes = []*url.URL{r1, r2}

	if !reflect.DeepEqual(golden, opts) {
		t.Fatalf("Options are incorrect.\nexpected: %+v\ngot: %+v",
			golden, opts)
	}
}

func TestServerRoutesWithClients(t *testing.T) {
	optsA, _ := ProcessConfigFile("./configs/srv_a.conf")
	optsB, _ := ProcessConfigFile("./configs/srv_b.conf")

	optsA.NoSigs, optsA.NoLog = true, true
	optsB.NoSigs, optsB.NoLog = true, true

	srvA := RunServer(optsA)
	defer srvA.Shutdown()

	urlA := fmt.Sprintf("nats://%s:%d/", optsA.Host, optsA.Port)
	urlB := fmt.Sprintf("nats://%s:%d/", optsB.Host, optsB.Port)

	nc1, err := nats.Connect(urlA)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc1.Close()

	ch := make(chan bool)
	sub, _ := nc1.Subscribe("foo", func(m *nats.Msg) { ch <- true })
	nc1.QueueSubscribe("foo", "bar", func(m *nats.Msg) {})
	nc1.Publish("foo", []byte("Hello"))
	// Wait for message
	<-ch
	sub.Unsubscribe()

	srvB := RunServer(optsB)
	defer srvB.Shutdown()

	// Wait for route to form.
	time.Sleep(250 * time.Millisecond)

	nc2, err := nats.Connect(urlB)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()
	nc2.Publish("foo", []byte("Hello"))
	nc2.Flush()
}

func TestServerRoutesWithAuthAndBCrypt(t *testing.T) {
	optsA, _ := ProcessConfigFile("./configs/srv_a_bcrypt.conf")
	optsB, _ := ProcessConfigFile("./configs/srv_b_bcrypt.conf")

	optsA.NoSigs, optsA.NoLog = true, true
	optsB.NoSigs, optsB.NoLog = true, true

	srvA := RunServer(optsA)
	defer srvA.Shutdown()

	srvB := RunServer(optsB)
	defer srvB.Shutdown()

	urlA := fmt.Sprintf("nats://%s:%d/", optsA.Host, optsA.Port)
	urlB := fmt.Sprintf("nats://%s:%d/", optsB.Host, optsB.Port)

	// Wait for route to form.
	time.Sleep(250 * time.Millisecond)

	nc1, err := nats.Connect(urlA)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc1.Close()

	// Test that we are connected.
	ch := make(chan bool)
	sub, _ := nc1.Subscribe("foo", func(m *nats.Msg) { ch <- true })
	nc1.Flush()
	defer sub.Unsubscribe()

	nc2, err := nats.Connect(urlB)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()
	nc2.Publish("foo", []byte("Hello"))

	// Wait for message
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message across route")
	}
}

// Helper function to check that a cluster is formed
func checkClusterFormed(t *testing.T, servers ...*Server) {
	// Wait for the cluster to form
	var err string
	expectedNumRoutes := len(servers) - 1
	maxTime := time.Now().Add(5 * time.Second)
	for time.Now().Before(maxTime) {
		err = ""
		for _, s := range servers {
			if numRoutes := s.NumRoutes(); numRoutes != expectedNumRoutes {
				err = fmt.Sprintf("Expected %d routes for server %q, got %d", expectedNumRoutes, s.ID(), numRoutes)
				break
			}
		}
		if err != "" {
			time.Sleep(100 * time.Millisecond)
		} else {
			break
		}
	}
	if err != "" {
		t.Fatalf("%s", err)
	}
}

// Helper function to generate next opts to make sure no port conflicts etc.
func nextServerOpts(opts *Options) *Options {
	nopts := *opts
	nopts.Port++
	nopts.ClusterPort++
	nopts.HTTPPort++
	return &nopts
}

func TestSeedSolicitWorks(t *testing.T) {
	optsSeed, _ := ProcessConfigFile("./configs/seed.conf")

	optsSeed.NoSigs, optsSeed.NoLog = true, true

	srvSeed := RunServer(optsSeed)
	defer srvSeed.Shutdown()

	optsA := nextServerOpts(optsSeed)
	optsA.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsSeed.ClusterHost, optsSeed.ClusterPort))

	srvA := RunServer(optsA)
	defer srvA.Shutdown()

	urlA := fmt.Sprintf("nats://%s:%d/", optsA.Host, optsA.Port)

	nc1, err := nats.Connect(urlA)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc1.Close()

	// Test that we are connected.
	ch := make(chan bool)
	nc1.Subscribe("foo", func(m *nats.Msg) { ch <- true })
	nc1.Flush()

	optsB := nextServerOpts(optsA)
	optsB.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsSeed.ClusterHost, optsSeed.ClusterPort))

	srvB := RunServer(optsB)
	defer srvB.Shutdown()

	urlB := fmt.Sprintf("nats://%s:%d/", optsB.Host, optsB.Port)

	nc2, err := nats.Connect(urlB)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()

	checkClusterFormed(t, srvSeed, srvA, srvB)

	nc2.Publish("foo", []byte("Hello"))

	// Wait for message
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message across route")
	}
}

func TestTLSSeedSolicitWorks(t *testing.T) {
	optsSeed, _ := ProcessConfigFile("./configs/seed_tls.conf")

	optsSeed.NoSigs, optsSeed.NoLog = true, true

	srvSeed := RunServer(optsSeed)
	defer srvSeed.Shutdown()

	optsA := nextServerOpts(optsSeed)
	optsA.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsSeed.ClusterHost, optsSeed.ClusterPort))

	srvA := RunServer(optsA)
	defer srvA.Shutdown()

	urlA := fmt.Sprintf("nats://%s:%d/", optsA.Host, optsA.Port)

	nc1, err := nats.Connect(urlA)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc1.Close()

	// Test that we are connected.
	ch := make(chan bool)
	nc1.Subscribe("foo", func(m *nats.Msg) { ch <- true })
	nc1.Flush()

	optsB := nextServerOpts(optsA)
	optsB.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsSeed.ClusterHost, optsSeed.ClusterPort))

	srvB := RunServer(optsB)
	defer srvB.Shutdown()

	urlB := fmt.Sprintf("nats://%s:%d/", optsB.Host, optsB.Port)

	nc2, err := nats.Connect(urlB)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()

	checkClusterFormed(t, srvSeed, srvA, srvB)

	nc2.Publish("foo", []byte("Hello"))

	// Wait for message
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message across route")
	}
}

func TestChainedSolicitWorks(t *testing.T) {
	optsSeed, _ := ProcessConfigFile("./configs/seed.conf")

	optsSeed.NoSigs, optsSeed.NoLog = true, true

	srvSeed := RunServer(optsSeed)
	defer srvSeed.Shutdown()

	optsA := nextServerOpts(optsSeed)
	optsA.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsSeed.ClusterHost, optsSeed.ClusterPort))

	srvA := RunServer(optsA)
	defer srvA.Shutdown()

	urlSeed := fmt.Sprintf("nats://%s:%d/", optsSeed.Host, optsSeed.Port)

	nc1, err := nats.Connect(urlSeed)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc1.Close()

	// Test that we are connected.
	ch := make(chan bool)
	nc1.Subscribe("foo", func(m *nats.Msg) { ch <- true })
	nc1.Flush()

	optsB := nextServerOpts(optsA)
	// Server B connects to A
	optsB.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsA.ClusterHost, optsA.ClusterPort))

	srvB := RunServer(optsB)
	defer srvB.Shutdown()

	urlB := fmt.Sprintf("nats://%s:%d/", optsB.Host, optsB.Port)

	nc2, err := nats.Connect(urlB)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()

	checkClusterFormed(t, srvSeed, srvA, srvB)

	nc2.Publish("foo", []byte("Hello"))

	// Wait for message
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message across route")
	}
}

func TestTLSChainedSolicitWorks(t *testing.T) {
	optsSeed, _ := ProcessConfigFile("./configs/seed_tls.conf")

	optsSeed.NoSigs, optsSeed.NoLog = true, true

	srvSeed := RunServer(optsSeed)
	defer srvSeed.Shutdown()

	optsA := nextServerOpts(optsSeed)
	optsA.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsSeed.ClusterHost, optsSeed.ClusterPort))

	srvA := RunServer(optsA)
	defer srvA.Shutdown()

	urlSeed := fmt.Sprintf("nats://%s:%d/", optsSeed.Host, optsSeed.Port)

	nc1, err := nats.Connect(urlSeed)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc1.Close()

	// Test that we are connected.
	ch := make(chan bool)
	nc1.Subscribe("foo", func(m *nats.Msg) { ch <- true })
	nc1.Flush()

	optsB := nextServerOpts(optsA)
	// Server B connects to A
	optsB.Routes = RoutesFromStr(fmt.Sprintf("nats://%s:%d", optsA.ClusterHost, optsA.ClusterPort))

	srvB := RunServer(optsB)
	defer srvB.Shutdown()

	urlB := fmt.Sprintf("nats://%s:%d/", optsB.Host, optsB.Port)

	nc2, err := nats.Connect(urlB)
	if err != nil {
		t.Fatalf("Error creating client: %v\n", err)
	}
	defer nc2.Close()

	checkClusterFormed(t, srvSeed, srvA, srvB)

	nc2.Publish("foo", []byte("Hello"))

	// Wait for message
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for message across route")
	}
}
