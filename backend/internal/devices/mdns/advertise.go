package mdns

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/net/dns/dnsmessage"
)

func announcement(e Entry, ttl uint32) (dnsmessage.Message, error) {
	service, err := dnsmessage.NewName(fqdn(e.Service))
	if err != nil {
		return dnsmessage.Message{}, err
	}
	instance, err := dnsmessage.NewName(e.Instance + "." + service.String())
	if err != nil {
		return dnsmessage.Message{}, err
	}
	host, err := dnsmessage.NewName(fqdn(e.Host))
	if err != nil {
		return dnsmessage.Message{}, err
	}
	fields := make([]string, 0, len(e.TXT))
	for k, v := range e.TXT {
		fields = append(fields, k+"="+v)
	}
	sort.Strings(fields)
	if len(fields) == 0 {
		fields = append(fields, "")
	}
	unique := dnsmessage.ClassINET | 0x8000
	return dnsmessage.Message{
		Header: dnsmessage.Header{Response: true, Authoritative: true},
		Answers: []dnsmessage.Resource{
			{Header: dnsmessage.ResourceHeader{Name: service, Class: dnsmessage.ClassINET, TTL: ttl}, Body: &dnsmessage.PTRResource{PTR: instance}},
			{Header: dnsmessage.ResourceHeader{Name: instance, Class: unique, TTL: ttl}, Body: &dnsmessage.SRVResource{Port: e.Port, Target: host}},
			{Header: dnsmessage.ResourceHeader{Name: instance, Class: unique, TTL: ttl}, Body: &dnsmessage.TXTResource{TXT: fields}},
			{Header: dnsmessage.ResourceHeader{Name: host, Class: unique, TTL: ttl}, Body: &dnsmessage.AResource{A: e.IP.As4()}},
		},
	}, nil
}

// Advertise 为开发接收端发布并应答一个服务，取消时发出 TTL 为零的离线公告。
func Advertise(ctx context.Context, e Entry) error {
	if !lan.IsLANAddress(e.IP) || e.Port == 0 {
		return errors.New("公告需要局域网 IPv4 地址和有效服务端口")
	}
	response, err := announcement(e, 120)
	if err != nil {
		return err
	}
	data, err := response.Pack()
	if err != nil {
		return err
	}
	links, loop, err := listLinks(e.IP)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		return errors.New("公告地址不属于本机的局域网组播网卡")
	}
	l := links[0]
	s, err := openSocket(l, loop)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.multicast(data, l); err != nil {
		return err
	}
	bye, err := announcement(e, 0)
	if err != nil {
		return err
	}
	goodbye, err := bye.Pack()
	if err != nil {
		return err
	}
	defer s.multicast(goodbye, l)
	stop := context.AfterFunc(ctx, func() { s.SetReadDeadline(time.Now()) })
	defer stop()
	buf := make([]byte, 9000)
	for ctx.Err() == nil {
		p, err := s.read(buf)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
		var query dnsmessage.Message
		if msgErr := query.Unpack(p.data); msgErr != nil || query.Response {
			continue
		}
		for _, q := range query.Questions {
			if !asksAbout(q, response.Answers) {
				continue
			}
			group := multicastGroup()
			dest := group
			payload := data
			if int(p.peer.Port()) != mdnsPort {
				// 传统单播查询使用请求的编号并回显问题。
				reply := response
				reply.ID, reply.Questions = query.ID, query.Questions
				payload, err = reply.Pack()
				if err != nil {
					break
				}
				dest = net.UDPAddrFromAddrPort(p.peer)
			} else if (!p.multicast || q.Class&0x8000 != 0) && p.peer.Addr() != e.IP {
				dest = net.UDPAddrFromAddrPort(p.peer)
			}
			replyLink := l
			if dest.IP.Equal(l.local.AsSlice()) {
				replyLink.iface.Index = 0
			} else if p.loop && dest == group {
				replyLink.iface.Index = s.loopIndex
			}
			s.send(payload, replyLink, dest)
			break
		}
	}
	return nil
}

func asksAbout(q dnsmessage.Question, records []dnsmessage.Resource) bool {
	if q.Class&0x7fff != dnsmessage.ClassINET {
		return false
	}
	for _, rr := range records {
		if strings.EqualFold(rr.Header.Name.String(), q.Name.String()) && (q.Type == rr.Header.Type || q.Type == dnsmessage.TypeALL) {
			return true
		}
	}
	return false
}
