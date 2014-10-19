package conn

import (
	"fmt"
	"sync"
	"testing"
	"time"

	peer "github.com/jbenet/go-ipfs/peer"

	context "github.com/jbenet/go-ipfs/Godeps/_workspace/src/code.google.com/p/go.net/context"
	ma "github.com/jbenet/go-ipfs/Godeps/_workspace/src/github.com/jbenet/go-multiaddr"
)

func tcpAddr(t *testing.T, port int) ma.Multiaddr {
	tcp, err := ma.NewMultiaddr(tcpAddrString(port))
	if err != nil {
		t.Fatal(err)
	}
	return tcp
}

func tcpAddrString(port int) string {
	return fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", port)
}

type msg struct {
	sent     bool
	received bool
	payload  string
}

func (m *msg) Sent(t *testing.T) {
	if m.sent {
		t.Fatal("sent msg at incorrect state:", m)
	}
	m.sent = true
}

func (m *msg) Received(t *testing.T) {
	if m.received {
		t.Fatal("received msg at incorrect state:", m)
	}
	m.received = true
}

type msgMap struct {
	sent int
	recv int
	msgs map[string]*msg
}

func (mm *msgMap) Sent(t *testing.T, payload string) {
	mm.msgs[payload].Sent(t)
	mm.sent++
}

func (mm *msgMap) Received(t *testing.T, payload string) {
	mm.msgs[payload].Received(t)
	mm.recv++
}

func genMessages(num int, tag string) *msgMap {
	msgs := &msgMap{msgs: map[string]*msg{}}
	for i := 0; i < num; i++ {
		s := fmt.Sprintf("Message #%d -- %s", i, tag)
		msgs.msgs[s] = &msg{payload: s}
	}
	return msgs
}

func setupMultiConns(t *testing.T, ctx context.Context) (a, b *MultiConn) {

	log.Info("Setting up peers")
	p1, err := setupPeer(tcpAddrString(11000))
	if err != nil {
		t.Fatal("error setting up peer", err)
	}

	p2, err := setupPeer(tcpAddrString(12000))
	if err != nil {
		t.Fatal("error setting up peer", err)
	}

	// peerstores
	p1ps := peer.NewPeerstore()
	p2ps := peer.NewPeerstore()

	// listeners
	listen := func(addr ma.Multiaddr, p *peer.Peer, ps peer.Peerstore) Listener {
		l, err := Listen(ctx, addr, p, ps)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}

	log.Info("Setting up listeners")
	p1l := listen(p1.Addresses[0], p1, p1ps)
	p2l := listen(p2.Addresses[0], p2, p2ps)

	// dialers
	p1d := &Dialer{Peerstore: p1ps, LocalPeer: p1}
	p2d := &Dialer{Peerstore: p2ps, LocalPeer: p2}

	dial := func(d *Dialer, dst *peer.Peer) <-chan Conn {
		cc := make(chan Conn)
		go func() {
			c, err := d.Dial(ctx, "tcp", dst)
			if err != nil {
				t.Fatal("error dialing peer", err)
			}
			cc <- c
		}()
		return cc
	}

	// connect simultaneously
	log.Info("Connecting...")
	p1dc := dial(p1d, p2)
	p2dc := dial(p2d, p1)

	c12a := <-p1l.Accept()
	c21a := <-p1dc
	c21b := <-p2l.Accept()
	c12b := <-p2dc

	log.Info("Ok, making multiconns")
	c1, err := NewMultiConn(ctx, p1, p2, []Conn{c12a, c12b})
	if err != nil {
		t.Fatal(err)
	}

	c2, err := NewMultiConn(ctx, p2, p1, []Conn{c21a, c21b})
	if err != nil {
		t.Fatal(err)
	}

	log.Info("dind you make multiconns?")
	return c1.(*MultiConn), c2.(*MultiConn)
}

func TestMulticonnSend(t *testing.T) {
	log.Info("TestMulticonnSend")
	ctx := context.Background()
	ctxC, cancel := context.WithCancel(ctx)

	c1, c2 := setupMultiConns(t, ctx)

	log.Info("gen msgs")
	num := 100
	msgsFrom1 := genMessages(num, "from p1 to p2")
	msgsFrom2 := genMessages(num, "from p2 to p1")

	var wg sync.WaitGroup

	send := func(c *MultiConn, msgs *msgMap) {
		for _, m := range msgs.msgs {
			log.Info("send: %s", m.payload)
			c.Out() <- []byte(m.payload)
			msgs.Sent(t, m.payload)
			<-time.After(time.Microsecond * 10)
		}

		wg.Done()
	}

	recv := func(ctx context.Context, c *MultiConn, msgs *msgMap) {
		for {
			select {
			case payload := <-c.In():
				msgs.Received(t, string(payload))
				log.Info("recv: %s", payload)
				<-time.After(time.Microsecond * 10)

			case <-ctx.Done():
				return

			}
		}

	}

	log.Info("msg send + recv")

	wg.Add(2)
	go send(c1, msgsFrom1)
	go send(c2, msgsFrom2)
	go recv(ctxC, c1, msgsFrom2)
	go recv(ctxC, c2, msgsFrom1)

	wg.Wait()
	<-time.After(time.Microsecond * 10)
	cancel()

	if msgsFrom1.sent != len(msgsFrom1.msgs) {
		t.Fatal("failed to send all msgs")
	}

	if msgsFrom2.sent != len(msgsFrom2.msgs) {
		t.Fatal("failed to send all msgs")
	}

	if msgsFrom1.recv != len(msgsFrom1.msgs) {
		t.Fatal("failed to recv all msgs")
	}

	if msgsFrom2.recv != len(msgsFrom2.msgs) {
		t.Fatal("failed to recv all msgs")
	}
}