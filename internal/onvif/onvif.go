package onvif

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"vartovyi/internal/model"
)

type Device struct {
	Address string `json:"address"`
}
type Profile struct {
	Token string `json:"token"`
	Name  string `json:"name"`
	URI   string `json:"uri"`
}

func Allowed(ctx context.Context, raw string, cidrs []string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Hostname() == "" {
		return errors.New("invalid camera URL")
	}
	ips, e := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if e != nil {
		return errors.New("camera DNS lookup failed")
	}
	if len(ips) == 0 {
		return errors.New("camera has no addresses")
	}
	for _, ip := range ips {
		ok := false
		for _, s := range cidrs {
			prefix, e := netip.ParsePrefix(s)
			if e == nil && prefix.Contains(ip.Unmap()) {
				ok = true
			}
		}
		if !ok {
			return errors.New("camera address is outside configured camera_cidrs")
		}
	}
	return nil
}
func Discover(ctx context.Context, iface string) ([]Device, error) {
	if iface == "" {
		return nil, errors.New("discovery_interface must be configured")
	}
	i, e := net.InterfaceByName(iface)
	if e != nil {
		return nil, e
	}
	addrs, e := i.Addrs()
	if e != nil {
		return nil, e
	}
	var local net.IP
	for _, a := range addrs {
		ip, _, _ := net.ParseCIDR(a.String())
		if ip.To4() != nil {
			local = ip.To4()
			break
		}
	}
	if local == nil {
		return nil, errors.New("interface has no IPv4 address")
	}
	conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: local})
	if e != nil {
		return nil, e
	}
	defer conn.Close()
	deadline := time.Now().Add(3 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	id := model.ID()
	uuid := id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
	message := `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl"><s:Header><a:MessageID>urn:uuid:` + uuid + `</a:MessageID><a:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To><a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</a:Action></s:Header><s:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></s:Body></s:Envelope>`
	if _, e = conn.WriteToUDP([]byte(message), &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 3702}); e != nil {
		return nil, e
	}
	out := []Device{}
	seen := map[string]bool{}
	buf := make([]byte, 64<<10)
	for len(out) < 128 {
		n, _, e := conn.ReadFromUDP(buf)
		if e != nil {
			break
		}
		var v struct {
			Body struct {
				Matches struct {
					Match []struct {
						XAddrs string `xml:"XAddrs"`
					} `xml:"ProbeMatch"`
				} `xml:"ProbeMatches"`
			} `xml:"Body"`
		}
		if xml.Unmarshal(buf[:n], &v) != nil {
			continue
		}
		for _, match := range v.Body.Matches.Match {
			for _, addr := range strings.Fields(match.XAddrs) {
				if !seen[addr] {
					seen[addr] = true
					out = append(out, Device{addr})
				}
			}
		}
	}
	return out, nil
}
func Profiles(ctx context.Context, address, user, password string, cidrs []string) ([]Profile, error) {
	if e := Allowed(ctx, address, cidrs); e != nil {
		return nil, e
	}
	u, e := url.Parse(address)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("ONVIF device address must be HTTP(S)")
	}
	security := wsse(user, password)
	b, e := soap(ctx, address, security, `<tds:GetCapabilities xmlns:tds="http://www.onvif.org/ver10/device/wsdl"><tds:Category>Media</tds:Category></tds:GetCapabilities>`)
	if e != nil {
		return nil, e
	}
	var caps struct {
		Body struct {
			Response struct {
				Capabilities struct {
					Media struct {
						Address string `xml:"XAddr"`
					} `xml:"Media"`
				} `xml:"Capabilities"`
			} `xml:"GetCapabilitiesResponse"`
		} `xml:"Body"`
	}
	if e = xml.Unmarshal(b, &caps); e != nil {
		return nil, e
	}
	endpoint := caps.Body.Response.Capabilities.Media.Address
	if e = Allowed(ctx, endpoint, cidrs); e != nil {
		return nil, e
	}
	b, e = soap(ctx, endpoint, wsse(user, password), `<trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`)
	if e != nil {
		return nil, e
	}
	var profiles struct {
		Body struct {
			Response struct {
				Profiles []struct {
					Token string `xml:"token,attr"`
					Name  string `xml:"Name"`
				} `xml:"Profiles"`
			} `xml:"GetProfilesResponse"`
		} `xml:"Body"`
	}
	if e = xml.Unmarshal(b, &profiles); e != nil {
		return nil, e
	}
	out := []Profile{}
	for i, p := range profiles.Body.Response.Profiles {
		if i >= 32 {
			break
		}
		var token bytes.Buffer
		_ = xml.EscapeText(&token, []byte(p.Token))
		body := `<trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><trt:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></trt:StreamSetup><trt:ProfileToken>` + token.String() + `</trt:ProfileToken></trt:GetStreamUri>`
		b, e = soap(ctx, endpoint, wsse(user, password), body)
		if e != nil {
			return nil, e
		}
		var stream struct {
			Body struct {
				Response struct {
					Media struct {
						URI string `xml:"Uri"`
					} `xml:"MediaUri"`
				} `xml:"GetStreamUriResponse"`
			} `xml:"Body"`
		}
		if e = xml.Unmarshal(b, &stream); e != nil {
			return nil, e
		}
		out = append(out, Profile{p.Token, p.Name, stream.Body.Response.Media.URI})
	}
	return out, nil
}
func wsse(user, password string) string {
	nonce := make([]byte, 20)
	_, _ = rand.Read(nonce)
	created := time.Now().UTC().Format(time.RFC3339Nano)
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(password))
	var name bytes.Buffer
	_ = xml.EscapeText(&name, []byte(user))
	return fmt.Sprintf(`<wsse:Security xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"><wsse:UsernameToken><wsse:Username>%s</wsse:Username><wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">%s</wsse:Password><wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">%s</wsse:Nonce><wsu:Created>%s</wsu:Created></wsse:UsernameToken></wsse:Security>`, name.String(), base64.StdEncoding.EncodeToString(h.Sum(nil)), base64.StdEncoding.EncodeToString(nonce), created)
}
func soap(ctx context.Context, address, security, body string) ([]byte, error) {
	u, e := url.Parse(address)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("invalid ONVIF endpoint")
	}
	data := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Header>` + security + `</s:Header><s:Body>` + body + `</s:Body></s:Envelope>`
	req, e := http.NewRequestWithContext(ctx, "POST", address, strings.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("ONVIF redirects disabled") }}
	resp, e := client.Do(req)
	if e != nil {
		return nil, errors.New("ONVIF request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ONVIF returned %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}
