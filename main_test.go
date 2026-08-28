package main

import (
	"net"
	"testing"
)

func TestResolveWSAddrUsesFreeBasePort(t *testing.T) {
	// Grab a free port, then release it so the base address is available.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	base := probe.Addr().String()
	probe.Close()

	ln, addr, err := resolveWSAddr(base, 10)
	if err != nil {
		t.Fatalf("resolveWSAddr: %v", err)
	}
	defer ln.Close()

	if addr != base {
		t.Fatalf("addr = %q, want %q (free base port should be used as-is)", addr, base)
	}
}

func TestResolveWSAddrIncrementsWhenTaken(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupied: %v", err)
	}
	defer occupied.Close()
	base := occupied.Addr().String()

	ln, addr, err := resolveWSAddr(base, 100)
	if err != nil {
		t.Fatalf("resolveWSAddr: %v", err)
	}
	defer ln.Close()

	if addr == base {
		t.Fatalf("addr = %q, expected a different port (base is occupied)", addr)
	}
}

func TestResolveWSAddrInvalidAddress(t *testing.T) {
	if _, _, err := resolveWSAddr("not-an-address", 10); err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestResolveWSAddrInvalidPort(t *testing.T) {
	if _, _, err := resolveWSAddr("127.0.0.1:notaport", 10); err == nil {
		t.Fatal("expected error for invalid port")
	}
}
