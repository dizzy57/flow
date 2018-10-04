package link

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/whiteboxio/flow/pkg/core"
)

const (
	MpxMsgSendTimeout = 50 * time.Millisecond
)

type MPX struct {
	Name  string
	links []core.Link
	*core.Connector
	*sync.Mutex
}

func New(name string, _ core.Params) (core.Link, error) {
	links := make([]core.Link, 0)
	mpx := &MPX{name, links, core.NewConnector(), &sync.Mutex{}}
	go mpx.multiplex()
	return mpx, nil
}

func (mpx *MPX) ConnectTo(core.Link) error {
	panic("MPX link is not supposed to be connected directly")
}

func (mpx *MPX) LinkTo(links []core.Link) error {
	mpx.Lock()
	defer mpx.Unlock()
	mpx.links = append(mpx.links, links...)
	return nil
}

func (mpx *MPX) multiplex() {
	for msg := range mpx.GetMsgCh() {
		mpx.Lock()
		linksLen := len(mpx.links)
		acks := make(chan core.MsgStatus, linksLen)
		ackChClosed := false
		msgMeta := msg.GetMetaAll()
		msgPayload := msg.Payload
		for _, link := range mpx.links {
			go func(l core.Link) {
				msgCp := core.NewMessageWithMeta(msgMeta, msgPayload)
				if sendErr := l.Recv(msgCp); sendErr != nil {
					acks <- core.MsgStatusFailed
					return
				}
				for ack := range msgCp.GetAckCh() {
					if !ackChClosed {
						acks <- ack
					}
				}
			}(link)
		}
		mpx.Unlock()
		ackCnt := 0
		failedCnt := 0
		for {
			if ackCnt == linksLen {
				break
			}
			select {
			case s := <-acks:
				ackCnt++
				if s != core.MsgStatusDone {
					failedCnt++
				}
			case <-time.After(MpxMsgSendTimeout):
				ackCnt++
				failedCnt++
			}
		}
		if failedCnt == 0 {
			msg.AckDone()
		} else if failedCnt == linksLen {
			msg.AckFailed()
		} else {
			msg.AckPartialSend()
		}
		ackChClosed = true
		for len(acks) > 0 {
			<-acks
		}
		close(acks)
	}
}

func Multiplex(msg *core.Message, links []core.Link, timeout time.Duration) error {
	var totalCnt, succCnt, failCnt uint32 = uint32(len(links)), 0, 0
	done := make(chan core.MsgStatus, totalCnt)
	doneClosed := false
	defer close(done)

	for _, l := range links {
		go func(link core.Link) {
			msgCp := core.CpMessage(msg)
			if err := link.Recv(msgCp); err != nil {
				atomic.AddUint32(&failCnt, 1)
				if !doneClosed {
					done <- core.MsgStatusFailed
				}
			}
			status := <-msgCp.GetAckCh()
			if !doneClosed {
				done <- status
			}
		}(l)
	}
	brk := time.After(MpxMsgSendTimeout)
	for i := 0; uint32(i) < totalCnt; i++ {
		select {
		case status := <-done:
			if status == core.MsgStatusDone {
				atomic.AddUint32(&succCnt, 1)
			} else {
				atomic.AddUint32(&failCnt, 1)
			}
		case <-brk:
			doneClosed = true
			close(done)
			break
		}
	}

	if failCnt > 0 {
		if succCnt == 0 {
			return msg.AckFailed()
		}
		return msg.AckPartialSend()
	}

	return msg.AckDone()
}
