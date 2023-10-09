package proxy

import (
	"NetManager/TableEntryCache"
	"NetManager/env"
	"NetManager/logger"
	"NetManager/proxy/iputils"
	"fmt"
	"math/rand"
	"net"
	"sync"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/songgao/water"
)

// const
var BUFFER_SIZE = 64 * 1024

// Config
type Configuration struct {
	HostTUNDeviceName         string
	ProxySubnetwork           string
	ProxySubnetworkMask       string
	TunNetIP                  string
	Mtusize                   string
	TunNetIPv6                string
	ProxySubnetworkIPv6       string
	TunnelPort                int
	ProxySubnetworkIPv6Prefix int
}

type GoProxyTunnel struct {
	Environment         env.EnvironmentManager
	listenConnection    *net.UDPConn
	incomingChannel     chan incomingMessage
	connectionBuffer    map[string]*net.UDPConn
	randseed            *rand.Rand
	ifce                *water.Interface
	outgoingChannel     chan outgoingMessage
	finishChannel       chan bool
	errorChannel        chan error
	stopChannel         chan bool
	HostTUNDeviceName   string
	tunNetIPv6          string
	tunNetIP            string
	mtusize             string
	ProxyIpSubnetwork   net.IPNet
	ProxyIPv6Subnetwork net.IPNet
	localIP             net.IP
	proxycache          ProxyCache
	TunnelPort          int
	bufferPort          int
	udpwrite            sync.RWMutex
	tunwrite            sync.RWMutex
	isListening         bool
}

// incoming message from UDP channel
type incomingMessage struct {
	content *[]byte
	from    net.UDPAddr
}

// outgoing message from bridge
type outgoingMessage struct {
	content *[]byte
}

func (proxy *GoProxyTunnel) GetIfce() *water.Interface {
	return proxy.ifce
}

// handler function for all outgoing messages that are received by the TUN device
func (proxy *GoProxyTunnel) outgoingMessage() {
	for {
		select {
		case msg := <-proxy.outgoingChannel:
			// logger.DebugLogger().Println("outgoingChannelSize: ", len(proxy.outgoingChannel))
			// logger.DebugLogger().Printf("Msg outgoingChannel: %x\n", (*msg.content))
			ip, prot := decodePacket(*msg.content)
			if ip == nil {
				continue
			}
			logger.DebugLogger().
				Printf("Outgoing packet:\t\t\t%s ---> %s\n", ip.SourceIP().String(), ip.DestinationIP().String())

			// continue only if the packet is udp or tcp, otherwise just drop it
			if prot == nil {
				logger.DebugLogger().Println("Neither TCP, nor UDP packet received. Dropping it.")
				continue
			}
			// proxyConversion
			newPacket := proxy.outgoingProxy(ip, prot)
			if newPacket == nil {
				// if no proxy conversion available, drop it
				logger.ErrorLogger().Println("Unable to convert the packet")
				continue
			}

			// fetch remote address
			dstHost, dstPort := proxy.locateRemoteAddress(ip.DestinationIP())

			// packetForwarding to tunnel interface
			proxy.forward(dstHost, dstPort, newPacket, 0)
		}
	}
}

// handler function for all ingoing messages that are received by the UDP socket
func (proxy *GoProxyTunnel) ingoingMessage() {
	for {
		select {
		case msg := <-proxy.incomingChannel:
			// logger.DebugLogger().Println("ingoingChannelSize: ", len(proxy.incomingChannel))
			// logger.DebugLogger().Printf("Msg incomingChannel: %x\n", (*msg.content))
			ip, prot := decodePacket(*msg.content)

			// proceed only if this is a valid ip packet
			if ip == nil {
				continue
			}
			logger.DebugLogger().
				Printf("Ingoing packet:\t\t\t %s <--- %s\n", ip.DestinationIP().String(), ip.SourceIP().String())

			// continue only if the packet is udp or tcp, otherwise just drop it
			if prot == nil {
				continue
			}

			// proxyConversion
			newPacket := proxy.ingoingProxy(ip, prot)
			var packetBytes []byte
			if newPacket == nil {
				// no conversion data, forward as is
				packetBytes = *msg.content
			} else {
				packetBytes = packetToByte(newPacket)
			}
			// output to bridge interface
			_, err := proxy.ifce.Write(packetBytes)
			if err != nil {
				logger.ErrorLogger().Println(err)
			}
		}
	}
}

// If packet destination is in the range of proxy.ProxyIpSubnetwork
// then find enable load balancing policy and find out the actual dstIP address
func (proxy *GoProxyTunnel) outgoingProxy(
	ip iputils.NetworkLayerPacket,
	prot iputils.TransportLayerProtocol,
) gopacket.Packet {
	dstIP := ip.DestinationIP()
	srcIP := ip.SourceIP()
	var semanticRoutingSubnetwork bool
	srcport := -1
	dstport := -1
	if prot != nil {
		srcport = int(prot.SourcePort())
		dstport = int(prot.DestPort())
	}

	// If packet destination is part of the semantic routing subnetwork let the proxy handle it
	if ip.ProtocolVersion() == 4 {
		semanticRoutingSubnetwork = proxy.ProxyIpSubnetwork.IP.Mask(proxy.ProxyIpSubnetwork.Mask).
			Equal(ip.DestinationIP().Mask(proxy.ProxyIpSubnetwork.Mask))
	}
	if ip.ProtocolVersion() == 6 {
		logger.DebugLogger().Println("DestIP len: ", len(ip.DestinationIP()))
		logger.DebugLogger().Println("ProxyIP len: ", len(proxy.ProxyIPv6Subnetwork.IP))
		semanticRoutingSubnetwork = proxy.ProxyIPv6Subnetwork.IP.Mask(proxy.ProxyIPv6Subnetwork.Mask).
			Equal(ip.DestinationIP().Mask(proxy.ProxyIPv6Subnetwork.Mask))
	}

	logger.DebugLogger().Println("semanticRoutingSubnetwork: ", semanticRoutingSubnetwork)
	if semanticRoutingSubnetwork {
		// Check if the ServiceIP is known
		tableEntryList := proxy.Environment.GetTableEntryByServiceIP(dstIP)
		logger.DebugLogger().Println("tableEntryList: ", tableEntryList)
		if len(tableEntryList) < 1 {
			return nil
		}

		// Find the instanceIP of the current service
		instanceIP, err := proxy.convertToInstanceIp(ip)
		logger.DebugLogger().Println("proxy Converted to Instance IP: ", instanceIP)
		if err != nil {
			return nil
		}

		// Check proxy proxycache (if any active flow is there already)
		entry, exist := proxy.proxycache.RetrieveByServiceIP(
			srcIP,
			instanceIP,
			srcport,
			dstIP,
			dstport,
		)
		logger.DebugLogger().Println("ProxyCache: RetrieveByServiceIP ", entry, exist)
		if !exist || entry.dstport < 1 ||
			!TableEntryCache.IsNamespaceStillValid(entry.dstip, &tableEntryList) {
			// Choose between the table entry according to the ServiceIP algorithm
			// TODO: so far this only uses RR, ServiceIP policies should be implemented here
			tableEntry := tableEntryList[proxy.randseed.Intn(len(tableEntryList))]

			entryDstIP := tableEntry.Nsipv6
			if ip.ProtocolVersion() == 4 {
				entryDstIP = tableEntry.Nsip
			}

			// Update proxycache
			entry = ConversionEntry{
				srcip:         srcIP,
				dstip:         entryDstIP,
				dstServiceIp:  dstIP,
				srcInstanceIp: instanceIP,
				srcport:       srcport,
				dstport:       dstport,
			}
			logger.DebugLogger().Println("Adding ConversionEntry: ", entry)
			proxy.proxycache.Add(entry)
		}
		return ip.SerializePacket(entry.dstip, entry.srcInstanceIp, prot)
	}
	return nil
}

func (proxy *GoProxyTunnel) convertToInstanceIp(ip iputils.NetworkLayerPacket) (net.IP, error) {
	instanceTableEntry, instanceexist := proxy.Environment.GetTableEntryByNsIP(ip.SourceIP())
	instanceIP := net.IP{}
	if instanceexist {
		for _, sip := range instanceTableEntry.ServiceIP {
			if sip.IpType == TableEntryCache.InstanceNumber {
				instanceIP = sip.Address_v6
				if ip.ProtocolVersion() == 4 {
					instanceIP = sip.Address
				}
			}
		}
	} else {
		logger.ErrorLogger().Println("Unable to find instance IP for service: ", ip.SourceIP())
		return nil, fmt.Errorf("unable to find instance IP for service: %s ", ip.SourceIP().String())
	}
	return instanceIP, nil
}

// If packet destination port is proxy.tunnelport then is a packet forwarded by the proxy. The src address must beù
// changed with he original packet destination
func (proxy *GoProxyTunnel) ingoingProxy(
	ip iputils.NetworkLayerPacket,
	prot iputils.TransportLayerProtocol,
) gopacket.Packet {
	dstport := -1
	srcport := -1

	if prot != nil {
		dstport = int(prot.DestPort())
		srcport = int(prot.SourcePort())
	}

	// Check proxy proxycache for REVERSE entry conversion
	// DstIP -> srcip, DstPort->srcport, srcport -> dstport
	entry, exist := proxy.proxycache.RetrieveByInstanceIp(ip.DestinationIP(), dstport, srcport)
	logger.DebugLogger().Println("ProxyCache: RetrieveByServiceIP ", entry, exist)

	if !exist {
		// No proxy proxycache entry, no translation needed
		return nil
	}

	// Reverse conversion
	return ip.SerializePacket(entry.srcip, entry.dstServiceIp, prot)
}

// Enable listening to outgoing packets
// if the goroutine must be stopped, send true to the stop channel
// when the channels finish listening a "true" is sent back to the finish channel
// in case of fatal error they are routed back to the err channel
func (proxy *GoProxyTunnel) tunOutgoingListen() {
	readerror := make(chan error)

	// async listener
	go proxy.ifaceread(proxy.ifce, proxy.outgoingChannel, readerror)

	// async handler
	go proxy.outgoingMessage()

	proxy.isListening = true
	logger.InfoLogger().Println("GoProxyTunnel outgoing listening started")
	for {
		select {
		case stopmsg := <-proxy.stopChannel:
			if stopmsg {
				logger.DebugLogger().Println("Outgoing listener received stop message")
				proxy.isListening = false
				proxy.finishChannel <- true
				return
			}
		case errormsg := <-readerror:
			proxy.errorChannel <- errormsg
		}
	}
}

// Enable listening for ingoing packets
// if the goroutine must be stopped, send true to the stop channel
// when the channels finish listening a "true" is sent back to the finish channel
// in case of fatal error they are routed back to the err channel
func (proxy *GoProxyTunnel) tunIngoingListen() {
	readerror := make(chan error)

	// async listener
	go proxy.udpread(proxy.listenConnection, proxy.incomingChannel, readerror)

	// async handler
	go proxy.ingoingMessage()

	proxy.isListening = true
	logger.InfoLogger().Println("GoProxyTunnel ingoing listening started")
	for {
		select {
		case stopmsg := <-proxy.stopChannel:
			if stopmsg {
				logger.DebugLogger().Println("Ingoing listener received stop message")
				_ = proxy.listenConnection.Close()
				proxy.isListening = false
				proxy.finishChannel <- true
				return
			}
		case errormsg := <-readerror:
			proxy.errorChannel <- errormsg
			// go udpread(proxy.listenConnection, readoutput, readerror)
		}
	}
}

// Given a network namespace IP find the machine IP and port for the tunneling
func (proxy *GoProxyTunnel) locateRemoteAddress(nsIP net.IP) (net.IP, int) {
	// if no local cache entry convert namespace IP to host IP via table query
	tableElement, found := proxy.Environment.GetTableEntryByNsIP(nsIP)
	if found {
		logger.DebugLogger().
			Println("Remote NS IP", nsIP.String(), " translated to ", tableElement.Nodeip.String(), tableElement.Nodeport)
		return tableElement.Nodeip, tableElement.Nodeport
	}

	// If nothing found, just drop the packet using an invalid port
	return nsIP, -1
}

// forward message to final destination via UDP tunneling
func (proxy *GoProxyTunnel) forward(
	dstHost net.IP,
	dstPort int,
	packet gopacket.Packet,
	attemptNumber int,
) {
	if attemptNumber > 10 {
		return
	}

	packetBytes := packetToByte(packet)

	// If destination host is this machine, forward packet directly to the ingoing traffic method
	if dstHost.Equal(proxy.localIP) {
		logger.DebugLogger().Println("Packet forwarded locally")
		msg := incomingMessage{
			from: net.UDPAddr{
				IP:   proxy.localIP,
				Port: 0,
				Zone: "",
			},
			content: &packetBytes,
		}
		proxy.incomingChannel <- msg
		return
	}

	// Check udp channel buffer to avoid creating a new channel
	proxy.udpwrite.Lock()
	hoststring := fmt.Sprintf("%s:%v", dstHost, dstPort)
	con, exist := proxy.connectionBuffer[hoststring]
	proxy.udpwrite.Unlock()
	// TODO: flush connection buffer by time to time
	if !exist {
		logger.DebugLogger().Println("Establishing a new connection to node ", hoststring)
		connection, err := createUDPChannel(hoststring)
		if nil != err {
			return
		}
		_ = connection.SetWriteBuffer(BUFFER_SIZE)
		proxy.udpwrite.Lock()
		proxy.connectionBuffer[hoststring] = connection
		proxy.udpwrite.Unlock()
		con = connection
	}

	logger.DebugLogger().Println("Sending to already known connection ", hoststring)
	// send via UDP channel
	proxy.udpwrite.Lock()
	_, _, err := (*con).WriteMsgUDP(packetBytes, nil, nil)
	proxy.udpwrite.Unlock()
	if err != nil {
		_ = (*con).Close()
		logger.ErrorLogger().Println(err)
		connection, err := createUDPChannel(hoststring)
		if nil != err {
			return
		}
		proxy.udpwrite.Lock()
		proxy.connectionBuffer[hoststring] = connection
		proxy.udpwrite.Unlock()
		// Try again
		attemptNumber++
		proxy.forward(dstHost, dstPort, packet, attemptNumber)
	}
}

func createUDPChannel(hoststring string) (*net.UDPConn, error) {
	raddr, err := net.ResolveUDPAddr("udp", hoststring)
	if err != nil {
		logger.ErrorLogger().Println("Unable to resolve remote addr:", err)
		return nil, err
	}
	connection, err := net.DialUDP("udp", nil, raddr)
	if nil != err {
		logger.ErrorLogger().Println("Unable to connect to remote addr:", err)
		return nil, err
	}
	err = connection.SetWriteBuffer(BUFFER_SIZE)
	if nil != err {
		logger.ErrorLogger().Println("Buffer error:", err)
		return nil, err
	}
	return connection, nil
}

// read output from an interface and wrap the read operation with a channel
// out channel gives back the byte array of the output
// errchannel is the channel where in case of error the error is routed
func (proxy *GoProxyTunnel) ifaceread(
	ifce *water.Interface,
	out chan<- outgoingMessage,
	errchannel chan<- error,
) {
	buffer := make([]byte, BUFFER_SIZE)
	for true {
		n, err := ifce.Read(buffer)
		if err != nil {
			errchannel <- err
		} else {
			res := make([]byte, n)
			copy(res, buffer[:n])
			logger.DebugLogger().Printf("Outgoing packet ready for decode action \n")
			out <- outgoingMessage{
				content: &res,
			}
		}
	}
}

// read output from an UDP connection and wrap the read operation with a channel
// out channel gives back the byte array of the output
// errchannel is the channel where in case of error the error is routed
func (proxy *GoProxyTunnel) udpread(
	conn *net.UDPConn,
	out chan<- incomingMessage,
	errchannel chan<- error,
) {
	buffer := make([]byte, BUFFER_SIZE)
	for true {
		packet := buffer
		n, from, err := conn.ReadFromUDP(packet)
		logger.DebugLogger().Println("Received UDP packet from: ", from)
		if err != nil {
			errchannel <- err
		} else {
			res := make([]byte, n)
			copy(res, buffer[:n])
			out <- incomingMessage{
				from:    *from,
				content: &res,
			}
		}
	}
}

func packetToByte(packet gopacket.Packet) []byte {
	options := gopacket.SerializeOptions{
		ComputeChecksums: false,
		FixLengths:       true,
	}
	newBuffer := gopacket.NewSerializeBuffer()
	err := gopacket.SerializePacket(newBuffer, options, packet)
	if err != nil {
		logger.ErrorLogger().Println(err)
	}
	return newBuffer.Bytes()
}

// GetName returns the name of the tun interface
func (proxy *GoProxyTunnel) GetName() string {
	return proxy.HostTUNDeviceName
}

// GetErrCh returns the error channel
// this channel sends all the errors of the tun device
func (proxy *GoProxyTunnel) GetErrCh() <-chan error {
	return proxy.errorChannel
}

// GetStopCh returns the errCh
// this channel is used to stop the service. After a shutdown the TUN device stops listening
func (proxy *GoProxyTunnel) GetStopCh() chan<- bool {
	return proxy.stopChannel
}

// GetFinishCh returns the confirmation that the channel stopped listening for packets
func (proxy *GoProxyTunnel) GetFinishCh() <-chan bool {
	return proxy.finishChannel
}

func decodePacket(msg []byte) (iputils.NetworkLayerPacket, iputils.TransportLayerProtocol) {
	// version == true -> IPv4 Header
	// version == false -> IPv6 Header
	var ipType layers.IPProtocol
	switch msg[0] & 0xf0 {
	case 0x40:
		ipType = layers.IPProtocolIPv4
	case 0x60:
		ipType = layers.IPProtocolIPv6
	default:
		logger.DebugLogger().Println("Received package was neither IPv4, nor IPv6 packet.")
		return nil, nil
	}

	packet := iputils.NewGoPacket(msg, ipType)
	if packet == nil {
		logger.DebugLogger().Println("Error decoding Network Layer of Packet")
	}

	ipLayer := packet.NetworkLayer()
	if ipLayer == nil {
		logger.ErrorLogger().Println("Network Layer could not have been decoded.")
		return nil, nil
	}

	res := iputils.NewNetworkLayerPacket(ipType, ipLayer)
	if ipType == layers.IPProtocolIPv6 {
		res.DecodeNetworkLayer(packet)
	}

	err := res.Defragment()
	if err != nil {
		logger.ErrorLogger().Println("Error in defragmentation")
		return nil, nil
	}

	return res, res.TransportLayer()
}