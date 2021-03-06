package receiver

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/awesome-flow/flow/pkg/core"
	"github.com/awesome-flow/flow/pkg/metrics"
	evio_rcv "github.com/awesome-flow/flow/pkg/receiver/evio"
	log "github.com/sirupsen/logrus"
)

const (
	ConnReadTimeout  = 1 * time.Second
	ConnWriteTimeout = 1 * time.Second
)

type replyMode uint8

const (
	replyModeSilent replyMode = iota
	replyModeTalkative
)

var (
	TcpRespAcpt = []byte("ACCEPTED")
	TcpRespSent = []byte("SENT")
	TcpRespPsnt = []byte("PART_SENT")
	TcpRespFail = []byte("FAILED")
	TcpRespInvd = []byte("INVALID")
	TcpRespTime = []byte("TIMEOUT")
	TcpRespUnrt = []byte("UNROUTABLE")
	TcpRespThrt = []byte("THROTTLED")

	ErrMalformedPacket = fmt.Errorf("Malformed packet")
	ErrEmptyBody       = fmt.Errorf("Empty message body")

	TcpMsgSendTimeout = 100 * time.Millisecond
)

type TCP struct {
	Name string
	mode replyMode
	addr string
	srv  net.Listener
	*core.Connector
}

func New(name string, params core.Params, context *core.Context) (core.Link, error) {
	tcpAddr, ok := params["bind_addr"]
	if !ok {
		return nil, fmt.Errorf("TCP receiver parameters are missing bind_addr")
	}
	mode := replyModeTalkative
	if alterMode, ok := params["mode"]; ok {
		switch alterMode {
		case "silent":
			mode = replyModeSilent
		case "talkative":
			mode = replyModeTalkative
		}
	}
	if backend, ok := params["backend"]; ok {
		switch backend {
		case "evio":
			log.Info("Instantiating Evio backend for TCP receiver")
			params["listeners"] = []interface{}{
				"tcp://" + params["bind_addr"].(string),
			}
			return evio_rcv.New(name, params, context)
		case "std":
		default:
			return nil, fmt.Errorf("Unknown backend: %s", backend)
		}
	}

	log.Info("Instantiating standard backend for TCP receiver")

	tcp := &TCP{
		name + "@" + tcpAddr.(string),
		mode,
		tcpAddr.(string),
		nil,
		core.NewConnector(),
	}

	tcp.OnSetUp(tcp.SetUp)
	tcp.OnTearDown(tcp.TearDown)

	return tcp, nil
}

func (tcp *TCP) SetUp() error {
	srv, err := net.Listen("tcp", tcp.addr)
	if err != nil {
		return err
	}
	tcp.srv = srv
	go tcp.handleListener()

	return nil
}

func (tcp *TCP) TearDown() error {
	if tcp.srv == nil {
		return fmt.Errorf("tcp listener is empty")
	}
	return tcp.srv.Close()
}

func (tcp *TCP) handleListener() {
	for {
		conn, err := tcp.srv.Accept()
		if err != nil {
			log.Errorf("TCP server failed to accept connection: %s", err.Error())
			continue
		}
		log.Infof("Received a new connection from %s", conn.RemoteAddr())
		go tcp.handleConnection(conn)
	}
}

func (tcp *TCP) handleConnection(conn net.Conn) {
	reader := bufio.NewReader(conn)

	metrics.GetCounter("receiver.tcp.conn.opened").Inc(1)

	for {
		conn.SetReadDeadline(time.Now().Add(ConnReadTimeout))
		data, err := reader.ReadBytes('\n')

		if len(data) == 0 {
			break
		}

		metrics.GetCounter("receiver.tcp.msg.received").Inc(1)

		if err != nil && err != io.EOF {
			log.Errorf("TCP receiver failed to read data: %s", err)
			metrics.GetCounter("receiver.tcp.conn.failed").Inc(1)
			conn.SetWriteDeadline(time.Now().Add(ConnWriteTimeout))
			conn.Write(TcpRespInvd)
			conn.Close()
			metrics.GetCounter("receiver.tcp.conn.closed").Inc(1)
			return
		}

		data = bytes.TrimRight(data, "\r\n")
		msg := core.NewMessage(data)

		if sendErr := tcp.Send(msg); sendErr != nil {
			metrics.GetCounter("receiver.tcp.msg.failed").Inc(1)
			log.Errorf("Failed to send message: %s", sendErr)
			tcp.replyWith(conn, TcpRespFail)
			continue
		}

		sync, ok := msg.Meta("sync")
		isSync := ok && (sync.(string) == "true" || sync.(string) == "1")
		if !isSync {
			metrics.GetCounter("receiver.tcp.msg.accepted").Inc(1)
			tcp.replyWith(conn, TcpRespAcpt)
			continue
		}

		select {
		case s := <-msg.GetAckCh():
			metrics.GetCounter(
				"receiver.tcp.msg.sent_" + strings.ToLower(string(status2resp(s)))).Inc(1)
			tcp.replyWith(conn, status2resp(s))
		case <-time.After(TcpMsgSendTimeout):
			metrics.GetCounter("receiver.tcp.msg.timed_out").Inc(1)
			tcp.replyWith(conn, TcpRespTime)
		}

		if err == io.EOF {
			break
		}
	}
	metrics.GetCounter("receiver.tcp.conn.closed").Inc(1)
	conn.Close()
}

func (tcp *TCP) replyWith(conn net.Conn, reply []byte) {
	if tcp.mode == replyModeSilent {
		return
	}
	conn.SetWriteDeadline(time.Now().Add(ConnWriteTimeout))
	conn.Write(reply)
}

func status2resp(s core.MsgStatus) []byte {
	switch s {
	case core.MsgStatusDone:
		return TcpRespSent
	case core.MsgStatusPartialSend:
		return TcpRespPsnt
	case core.MsgStatusInvalid:
		return TcpRespInvd
	case core.MsgStatusFailed:
		return TcpRespFail
	case core.MsgStatusTimedOut:
		return TcpRespTime
	case core.MsgStatusUnroutable:
		return TcpRespUnrt
	case core.MsgStatusThrottled:
		return TcpRespThrt
	default:
		return []byte("This should not happen")
	}
}
