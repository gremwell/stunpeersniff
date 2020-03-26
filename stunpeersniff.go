package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"

	"gortc.io/stun"
)

var wg sync.WaitGroup

func runStunDemuxTool(stunDemuxTool string, peerAddr string, peerPort uint16, localAddr string, localPort uint16) {
	cmd := exec.Command(stunDemuxTool,
		"-H", peerAddr,
		"-P", strconv.FormatUint(uint64(peerPort), 10),
		"-h", localAddr,
		"-p", strconv.FormatUint(uint64(localPort), 10))

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Run()
}

var demuxToolLaunched bool

func searchStunPeer(buf []byte, stunDemuxTool string) {
	var m stun.Message
	if err := stun.Decode(buf, &m); err != nil {
		return
	}

	for _, att := range m.Attributes {
		if att.Type == stun.AttrXORPeerAddress {
			// magic cookie is fixed to 0x2112A442
			peerPort := binary.BigEndian.Uint16(att.Value[2:4]) ^ 0x2112

			peerIpAddr := make(net.IP, 4)
			peerIpAddr[0] = att.Value[4] ^ 0x21
			peerIpAddr[1] = att.Value[5] ^ 0x12
			peerIpAddr[2] = att.Value[6] ^ 0xA4
			peerIpAddr[3] = att.Value[7] ^ 0x42

			// run UDP proxy in background
			if !demuxToolLaunched {
				demuxToolLaunched = true

				log.Printf("peer address: %s:%d\n", peerIpAddr, peerPort)
				log.Printf("launching demux tool (%s)...\n", stunDemuxTool)

				// run it only once
				go runStunDemuxTool(stunDemuxTool,
					peerIpAddr.String(), peerPort,
					"127.0.0.1", uint16(6001))
			}
		}
	}
}

func tcpProxyConnection(remoteAddr string, conn *net.TCPConn, stunDemuxTool string) {
	rAddr, err := net.ResolveTCPAddr("tcp", remoteAddr)
	if err != nil {
		log.Fatal(err)
	}

	rConn, err := net.DialTCP("tcp", nil, rAddr)
	if err != nil {
		log.Fatal(err)
	}

	defer rConn.Close()

	// Request loop
	go func() {
		for {
			data := make([]byte, 1024*1024)

			n, err := conn.Read(data)
			if err != nil {
				log.Fatal(err)
			}

			rConn.Write(data[:n])

			searchStunPeer(data[:n], stunDemuxTool)
		}
	}()

	// Response loop
	for {
		data := make([]byte, 1024*1024)

		n, err := rConn.Read(data)
		if err != nil {
			log.Fatal(err)
		}
		conn.Write(data[:n])

		searchStunPeer(data[:n], stunDemuxTool)
	}
}

func main() {
	var remoteAddr *string = flag.String("H", "localhost", "remote server address")
	var remotePort *int = flag.Int("P", 3478, "remote server port")
	var localAddr *string = flag.String("h", "localhost", "address to bind to")
	var localPort *int = flag.Int("p", 6000, "local proxy port")

	var stunDemuxTool *string = flag.String("t", "stundemux", "tool to launch if peer address found")

	flag.Parse()

	log.SetOutput(os.Stdout)

	tcpProxyRemoteAddress := *remoteAddr
	tcpProxyRemotePort := uint16(*remotePort)
	tcpProxyListenAddress := *localAddr
	tcpProxyListenPort := uint16(*localPort)

	demuxToolLaunched = false

	addr, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("%s:%d", tcpProxyListenAddress, tcpProxyListenPort))
	if err != nil {
		log.Fatal(err)
	}

	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}

	conn, err := listener.AcceptTCP()
	if err != nil {
		log.Fatal(err)
	}

	tcpProxyConnection(fmt.Sprintf("%s:%d", tcpProxyRemoteAddress, tcpProxyRemotePort), conn, *stunDemuxTool)
}
