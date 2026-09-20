package api

import (
	"net/http"
	"net/netip"
	"strings"
)

const headerXForwardedFor = "X-Forwarded-For"

// clientIPResolver는 요청을 보낸 클라이언트의 주소를 가려낸다.
//
// 운영에서는 서버 앞에 프록시(K3s의 Traefik)가 있어서 연결의 상대 주소는 늘 프록시다.
// 진짜 주소는 프록시가 X-Forwarded-For에 적어 준다. 그런데 이 헤더는 누구나 지어내서 보낼 수 있다.
// 그래서 연결의 상대가 믿는 프록시일 때만 헤더를 보고, 헤더 안에서도 믿는 프록시가 적은 부분만 믿는다.
// 헤더를 그냥 믿으면 요청마다 다른 주소를 지어내 시도 한도를 피해 갈 수 있다.
//
// 믿는 프록시가 없으면(기본값) 헤더를 아예 보지 않는다.
type clientIPResolver struct {
	trusted []netip.Prefix
}

func newClientIPResolver(trusted []netip.Prefix) clientIPResolver {
	masked := make([]netip.Prefix, 0, len(trusted))
	for _, p := range trusted {
		if p.IsValid() {
			masked = append(masked, p.Masked())
		}
	}
	return clientIPResolver{trusted: masked}
}

// resolve는 클라이언트의 주소를 돌려준다. 알 수 없으면 빈 값이다.
//
// 프록시는 자기가 본 상대 주소를 헤더의 맨 뒤에 덧붙인다. 그러므로 뒤에서부터 읽으면서 믿는 프록시의 주소를 건너뛰고,
// 처음 만나는 믿지 않는 주소가 클라이언트다. 그보다 앞의 값은 클라이언트가 지어냈을 수 있으므로 보지 않는다.
func (r clientIPResolver) resolve(req *http.Request) netip.Addr {
	peer := parseHostAddr(req.RemoteAddr)
	if !peer.IsValid() || !r.isTrusted(peer) {
		return peer
	}

	client := peer
	hops := strings.Split(strings.Join(req.Header.Values(headerXForwardedFor), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop == "" {
			continue
		}
		addr := parseHostAddr(hop)
		if !addr.IsValid() {
			// 읽을 수 없는 값의 앞쪽은 누가 적었는지 알 수 없다. 여기까지 확인한 주소에서 멈춘다.
			return client
		}
		client = addr
		if !r.isTrusted(addr) {
			return client
		}
	}
	// 헤더가 없거나 모두 믿는 프록시의 주소다. 가장 멀리서 온 주소를 쓴다.
	return client
}

func (r clientIPResolver) isTrusted(addr netip.Addr) bool {
	for _, p := range r.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseHostAddr는 "주소" 또는 "주소:포트"를 읽는다. IPv4를 IPv6에 담은 꼴(::ffff:1.2.3.4)은 IPv4로 풀고,
// 같은 기기가 구역 이름만 바꿔 다른 주소로 보이지 않게 구역(%eth0)은 뗀다.
func parseHostAddr(s string) netip.Addr {
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap().WithZone("")
	}
	addr, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap().WithZone("")
}
