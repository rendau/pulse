package service

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/rendau/pulse/internal/domain/edge/model"
)

// ipv6PrefixBits — адреса IPv6 считаются по подсети /64: у мобильного клиента адрес внутри
// своей /64 меняется (временные адреса), а атакующий распоряжается подсетью целиком.
const ipv6PrefixBits = 64

// ClientKey — адрес клиента, по которому считается трафик: IPv4 как есть, IPv6 — подсеть /64
// (2a03:32c0:e000:2545::/64). Уже подсеть — приводится к тому же виду. Не адрес — "" (false).
func ClientKey(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if p, err := netip.ParsePrefix(s); err == nil {
		addr := p.Addr().Unmap()
		if addr.Is4() {
			return netip.PrefixFrom(addr, min(p.Bits(), 32)).Masked().String(), true
		}
		return netip.PrefixFrom(addr, min(p.Bits(), ipv6PrefixBits)).Masked().String(), true
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return "", false
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String(), true
	}
	return netip.PrefixFrom(addr.WithZone(""), ipv6PrefixBits).Masked().String(), true
}

// keyPrefix — ключ клиента как подсеть (IPv4 — /32).
func keyPrefix(key string) (netip.Prefix, bool) {
	if p, err := netip.ParsePrefix(key); err == nil {
		return p, true
	}
	addr, err := netip.ParseAddr(key)
	if err != nil {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, addr.BitLen()), true
}

// ParseKnown разбирает список своих адресов «адрес_или_подсеть=метка» (метка необязательна).
// Ошибочные элементы пропускаются и возвращаются вторым значением — для лога.
func ParseKnown(entries []string) ([]model.Known, []string) {
	var known []model.Known
	var bad []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		addr, label, _ := strings.Cut(entry, "=")
		addr, label = strings.TrimSpace(addr), strings.TrimSpace(label)
		prefix, ok := keyPrefix(addr)
		if !ok {
			bad = append(bad, fmt.Sprintf("%q: not an address or subnet", entry))
			continue
		}
		known = append(known, model.Known{Prefix: prefix.Masked(), Label: label})
	}
	return known, bad
}

// KnownLabel — метка своего адреса для ключа клиента (адрес или подсеть /64): первая подсеть
// списка, пересекающаяся с ним. Метка пустая у своего адреса без метки — тогда «свой».
func KnownLabel(known []model.Known, key string) (string, bool) {
	p, ok := keyPrefix(key)
	if !ok {
		return "", false
	}
	for _, k := range known {
		if k.Prefix.Overlaps(p) {
			if k.Label == "" {
				return "свой", true
			}
			return k.Label, true
		}
	}
	return "", false
}
