package iputils

import (
	"NetManager/logger"
	"fmt"
	"net"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	// "github.com/sipcapture/heplify/ip6defrag"
)

type IPv6Packet struct {
	*layers.IPv6
	*layers.IPv6Fragment
}

// IPv6 defragger
// var v6defragger = ip6defrag.NewIPv6Defragmenter()

func newIPv6Packet(nl gopacket.NetworkLayer) NetworkLayerPacket {
	return &IPv6Packet{
		IPv6:         nl.(*layers.IPv6),
		IPv6Fragment: &layers.IPv6Fragment{},
	}
}

func (packet *IPv6Packet) isNetworkLayer() bool {
	return true
}

func (packet *IPv6Packet) Layer() gopacket.Layer {
	return packet.IPv6
}

func (packet *IPv6Packet) ProtocolVersion() uint8 {
	return packet.Version
}

func (packet *IPv6Packet) NextHeader() uint8 {
	return uint8(packet.IPv6.NextHeader)
}

func (packet *IPv6Packet) DecodeNetworkLayer(gop gopacket.Packet) {
	ipv6 := gop.Layer(layers.LayerTypeIPv6)
	ipv6Fields := ipv6.(*layers.IPv6)
	var ipv6FragmentFields *layers.IPv6Fragment
	if ipv6Fields.NextHeader != layers.IPProtocolIPv6Fragment {
		return
	}

	ipv6Fragment := gop.Layer(layers.LayerTypeIPv6Fragment)
	if ipv6Fragment != nil {
		ipv6FragmentFields = ipv6Fragment.(*layers.IPv6Fragment)
	}
	packet.IPv6Fragment = ipv6FragmentFields
}

func (packet *IPv6Packet) Defragment() error {
	// FIXME: if needed
	/*
		ipv6Defrag, err := v6defragger.DefragIPv6(packet.IPv6, packet.IPv6Fragment)
		if err != nil {
			fmt.Println(err)
			return err
		} else if ipv6Defrag == nil {
			return fmt.Errorf("packet was a fragment. Saved state and waiting for rest")
		}
		packet.IPv6 = ipv6Defrag
		return nil
	*/
	// overwrites NextHeader Value for whatever reason
	return nil
}

func (packet *IPv6Packet) TransportLayer() TransportLayerProtocol {
	switch packet.IPv6.NextHeader {
	case layers.IPProtocolUDP:
		udplayer := packet.IPv6.LayerPayload()
		// TODO: create factory
		udp := &UDPLayer{&layers.UDP{}}
		err := udp.DecodeFromBytes(udplayer, gopacket.NewDecodingLayerParser(layers.LayerTypeUDP))
		if err != nil {
			logger.ErrorLogger().Println("Could not decode IPv6 UDP packet.")
		}
		return udp
	case layers.IPProtocolTCP:
		tcplayer := packet.IPv6.LayerPayload()
		tcp := &TCPLayer{&layers.TCP{}}
		err := tcp.DecodeFromBytes(tcplayer, gopacket.NewDecodingLayerParser(layers.LayerTypeTCP))
		if err != nil {
			logger.ErrorLogger().Println("Could not decode IPv6 TCP packet.")
		}
		return tcp
	default:
		logger.ErrorLogger().Println("Could not determine TransportLayer of IPv6 Packet.")
		return nil
	}
}

func (ip *IPv6Packet) SerializePacket(dstIp net.IP, srcIp net.IP, prot TransportLayerProtocol) gopacket.Packet {
	ip.DstIP = dstIp
	ip.SrcIP = srcIp

	if prot.Protocol() == "TCP" {
		return ip.serializeTCPHeader(prot.TCPLayer())
	} else {
		return ip.serializeUDPHeader(prot.UDPLayer())
	}
}

func (ip *IPv6Packet) serializeTCPHeader(tcp *layers.TCP) gopacket.Packet {
	err := tcp.SetNetworkLayerForChecksum(ip.IPv6)
	if err != nil {
		fmt.Println(err)
	}
	return ip.serializeIPHeader(tcp, gopacket.Payload(tcp.Payload))
}

func (ip *IPv6Packet) serializeUDPHeader(udp *layers.UDP) gopacket.Packet {
	err := udp.SetNetworkLayerForChecksum(ip.IPv6)
	if err != nil {
		fmt.Println(err)
	}
	return ip.serializeIPHeader(udp, gopacket.Payload(udp.Payload))
}

func (ip *IPv6Packet) serializeIPHeader(transportLayer gopacket.SerializableLayer, payload gopacket.SerializableLayer) gopacket.Packet {
	newBuffer := gopacket.NewSerializeBuffer()
	err := ip.SerializeTo(newBuffer, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true})
	if err != nil {
		fmt.Println(err)
	}

	buffer := gopacket.NewSerializeBuffer()
	err = gopacket.SerializeLayers(
		buffer,
		gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
		ip.IPv6,
		transportLayer,
		payload,
	)
	if err != nil {
		fmt.Printf("packet serialization failure %v\n", err)
		return nil
	}

	return gopacket.NewPacket(buffer.Bytes(), layers.LayerTypeIPv6, gopacket.Default)
}

func (packet *IPv6Packet) DestinationIP() net.IP {
	return packet.DstIP
}

func (packet *IPv6Packet) SourceIP() net.IP {
	return packet.SrcIP
}
