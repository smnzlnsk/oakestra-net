package network

import (
	"NetManager/logger"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"time"

	"tailscale.com/net/interfaces"
)

// GetLocalIP returns the non loopback local IP of the host and the associated interface
func GetLocalIPandIface() (string, string) {
	list, err := net.Interfaces()
	if err != nil {
		log.Printf("not net Interfaces found")
		panic(err)
	}
	defaultIfce, err := interfaces.DefaultRouteInterface()
	if err != nil {
		log.Printf("not default Interfaces found")
		panic(err)
	}

	for _, iface := range list {
		addrs, err := iface.Addrs()
		if err != nil {
			panic(err)
		}
		for idx, address := range addrs {
			logger.InfoLogger().Printf("idx: %d IP: %s", idx, address.String())
			// check the address type and if it is not a loopback the display it
			if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && iface.Name == defaultIfce {
				// TODO: DISCUSS: Should we first check for IPv6 on the interface first and fallback to v4?
				if ipnet.IP.To4() != nil {
					log.Println("Local Interface in use: ", iface.Name, " with addr ", ipnet.IP.String())
					return ipnet.IP.String(), iface.Name
				}
			}
		}
	}

	return "", ""
}

// https://gist.github.com/schwarzeni/f25031a3123f895ff3785970921e962c
func GetInterfaceIPByName(interfaceName string) (addresses []net.IP, err error) {
	var (
		ief      *net.Interface
		addrs    []net.Addr
		ipv4Addr net.IP
		ipv6Addr net.IP
	)
	if ief, err = net.InterfaceByName(interfaceName); err != nil { // get interface
		return nil, err
	}
	if addrs, err = ief.Addrs(); err != nil { // get addresses
		return nil, err
	}
	for _, addr := range addrs { // get ipv4 address
		if ipv4Addr = addr.(*net.IPNet).IP.To4(); ipv4Addr != nil {
			addresses = append(addresses, ipv4Addr)
			continue
		}
		if ipv6Addr = addr.(*net.IPNet).IP.To16(); ipv6Addr != nil && !ipv6Addr.IsLinkLocalMulticast() && !ipv6Addr.IsLinkLocalUnicast() {
			addresses = append(addresses, ipv6Addr)
		}
	}
	if addresses == nil {
		return nil, fmt.Errorf("interface %s don't have any addresses", interfaceName)
	}
	return addresses, nil
}

func NameUniqueHash(name string, size int) string {
	shaHashFunc := sha1.New()
	fmt.Fprintf(shaHashFunc, "%s,%s", time.Now().String(), name)
	hashed := shaHashFunc.Sum(nil)
	for size > len(hashed) {
		hashed = append(hashed, hashed...)
	}
	hashedAndEncoded := base64.URLEncoding.EncodeToString(hashed)
	return hashedAndEncoded[:size]
}

// Given an IP, give IP+inc
// FIXME: rework, since it is hacky and could cause problems
func NextIP(ip net.IP, inc uint) net.IP {
	ipBytes := ip.To16()
	for i := len(ipBytes) - 1; i >= 0; i-- {
		if ipBytes[i] == 255 {
			ipBytes[i] = 0
		} else {
			ipBytes[i] = ipBytes[i] + byte(inc)
			break
		}
	}
	return net.IP(ipBytes)
}

//Given an ipv4, gives the next IP
/*
func NextIP(ip net.IP, inc uint) net.IP {
	i := ip.To4()
	v := uint(i[0])<<24 + uint(i[1])<<16 + uint(i[2])<<8 + uint(i[3])
	v += inc
	v3 := byte(v & 0xFF)
	v2 := byte((v >> 8) & 0xFF)
	v1 := byte((v >> 16) & 0xFF)
	v0 := byte((v >> 24) & 0xFF)
	return net.IPv4(v0, v1, v2, v3)
}

func NextIPv6(ip net.IP, inc uint) net.IP {
	i := ip.To16()

	// transform IP address to 128 bit Integer and increment by one
	ipInt := new(big.Int).SetBytes(i)
	ipInt.Add(ipInt, big.NewInt(1))

	// transform new incremented IP address back to net.IP format and return
	ret := make(net.IP, net.IPv6len)
	ipInt.FillBytes(ret)

	return ret
}*/