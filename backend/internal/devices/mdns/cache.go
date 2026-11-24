package mdns

import (
	"maps"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func fqdn(name string) string {
	return strings.TrimSuffix(name, ".") + "."
}

type rrKey struct {
	name string
	typ  dnsmessage.Type
}

type record struct {
	body    dnsmessage.ResourceBody
	expires time.Time
}

// 缓存按报文来源和接收网卡隔离。PTR 按目标实例区分，同名服务可有多个实例。
type peerCache struct {
	records   map[rrKey]record
	published map[string]Entry
	asked     map[rrKey]time.Time
}

func newPeerCache() *peerCache {
	return &peerCache{records: make(map[rrKey]record), published: make(map[string]Entry), asked: make(map[rrKey]time.Time)}
}

func (c *peerCache) ingest(service string, ip netip.Addr, records []dnsmessage.Resource, now time.Time) {
	for _, rr := range records {
		if rr.Header.Class&0x7fff != dnsmessage.ClassINET {
			continue
		}
		name := strings.ToLower(rr.Header.Name.String())
		switch data := rr.Body.(type) {
		case *dnsmessage.PTRResource:
			if name != service {
				continue
			}
			name = strings.ToLower(data.PTR.String())
			if !strings.HasSuffix(name, "."+service) {
				continue
			}
		case *dnsmessage.SRVResource:
			if !strings.HasSuffix(name, "."+service) || data.Port == 0 {
				continue
			}
		case *dnsmessage.TXTResource:
			if !strings.HasSuffix(name, "."+service) {
				continue
			}
		case *dnsmessage.AResource:
			if ip != netip.AddrFrom4(data.A) {
				continue
			}
		default:
			continue
		}
		key := rrKey{name, rr.Header.Type}
		if rr.Header.TTL == 0 {
			delete(c.records, key)
			continue
		}
		if len(c.records) >= 256 {
			if _, ok := c.records[key]; !ok {
				continue
			}
		}
		c.records[key] = record{rr.Body, now.Add(time.Duration(rr.Header.TTL) * time.Second)}
	}
}

func (c *peerCache) prune(now time.Time) {
	for key, retry := range c.asked {
		if !now.Before(retry) {
			delete(c.asked, key)
		}
	}
	for key, rr := range c.records {
		if !now.Before(rr.expires) {
			delete(c.records, key)
		}
	}
}

func (c *peerCache) resolve(service string, l link, now time.Time, receive func(Entry)) []dnsmessage.Question {
	var missing []dnsmessage.Question
	seen := make(map[string]bool)
	for k, ptr := range c.records {
		if k.typ != dnsmessage.TypePTR {
			continue
		}
		instance := ptr.body.(*dnsmessage.PTRResource).PTR
		srv, hasSRV := c.records[rrKey{k.name, dnsmessage.TypeSRV}]
		txt, hasTXT := c.records[rrKey{k.name, dnsmessage.TypeTXT}]
		if !hasTXT {
			missing = append(missing, question(instance, dnsmessage.TypeTXT))
		}
		if !hasSRV {
			missing = append(missing, question(instance, dnsmessage.TypeSRV))
			continue
		}
		target := srv.body.(*dnsmessage.SRVResource)
		a, hasA := c.records[rrKey{strings.ToLower(target.Target.String()), dnsmessage.TypeA}]
		if !hasA {
			missing = append(missing, question(target.Target, dnsmessage.TypeA))
		}
		if !hasTXT || !hasA {
			continue
		}
		values := make(map[string]string)
		for _, s := range txt.body.(*dnsmessage.TXTResource).TXT {
			key, val, _ := strings.Cut(s, "=")
			values[strings.ToLower(key)] = val
		}
		entry := Entry{
			Service: service, Instance: instance.String()[:len(instance.String())-len(service)-1],
			Host: target.Target.String(), Port: target.Port, IP: netip.AddrFrom4(a.body.(*dnsmessage.AResource).A),
			LocalIP: l.local, InterfaceIndex: l.iface.Index, TXT: values, Expires: ptr.expires,
		}
		for _, rr := range []record{srv, txt, a} {
			if rr.expires.Before(entry.Expires) {
				entry.Expires = rr.expires
			}
		}
		seen[k.name] = true
		prev, exists := c.published[k.name]
		if !exists || !prev.Expires.Equal(entry.Expires) || prev.Host != entry.Host || prev.Port != entry.Port || !maps.Equal(prev.TXT, values) {
			c.published[k.name] = entry
			receive(entry)
		}
	}
	for name, prev := range c.published {
		if !seen[name] {
			delete(c.published, name)
			prev.Expires = now
			receive(prev)
		}
	}
	return missing
}
