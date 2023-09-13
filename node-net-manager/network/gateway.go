package network

import (
	"NetManager/logger"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"

	"github.com/coreos/go-iptables/iptables"
	"github.com/songgao/water"
)

type ServiceExposer interface {
	EnableServiceExposure(string, net.IP, int, int) error
	DisableServiceExposure(string) error
}

type FirewallConfiguration struct {
	FirewallID      string
	publicIPv4      net.IP
	publicIPv6      net.IP
	publicInterface string
	exposedServices map[string]ServiceEntry // map[key: string - serviceID]value: ServiceEntry
	exposedPorts    map[int]bool
	iptable4        IpTable
	iptable6        IpTable
}

type GatewayConfiguration struct {
	GatewayID         string
	publicIPv4        net.IP
	publicIPv6        net.IP
	publicInterface   string
	oakestraIPv4      net.IP
	oakestraIPv6      net.IP
	oakestraInterface *water.Interface
	exposedServices   map[string]ServiceEntry
	exposedPorts      map[int]bool
	iptable4          IpTable
	iptable6          IpTable
}

type ServiceEntry struct {
	serviceIP   net.IP
	servicePort int
	exposedPort int
}

var Exposer ServiceExposer

func StartFirewallProcess(id string, ipv4 net.IP, ipv6 net.IP) *FirewallConfiguration {
	config := &FirewallConfiguration{
		FirewallID:      id,
		publicIPv4:      ipv4,
		publicIPv6:      ipv6,
		publicInterface: "", // TODO
		exposedServices: make(map[string]ServiceEntry),
		exposedPorts:    make(map[int]bool),
		iptable4:        NewOakestraIPTable(iptables.ProtocolIPv4),
		iptable6:        NewOakestraIPTable(iptables.ProtocolIPv6),
	}
	Exposer = config
	return config
}

func StartGatewayProcess(id string, publicIPv4 net.IP, publicIPv6 net.IP, oakIPv4 net.IP, oakIPv6 net.IP) *GatewayConfiguration {
	ip, ifaceName := GetLocalIPandIface()
	if publicIPv4.String() != ip && publicIPv6.String() != ip {
		return nil
	}
	logger.InfoLogger().Println("Configuring Gateway")
	config := &GatewayConfiguration{
		GatewayID:         id,
		publicIPv4:        publicIPv4,
		publicIPv6:        publicIPv6,
		publicInterface:   ifaceName, // TODO
		oakestraIPv4:      oakIPv4,
		oakestraIPv6:      oakIPv6,
		oakestraInterface: newGatewayInterface(oakIPv4, oakIPv6),
		exposedServices:   make(map[string]ServiceEntry),
		exposedPorts:      make(map[int]bool),
		iptable4:          NewOakestraIPTable(iptables.ProtocolIPv4),
		iptable6:          NewOakestraIPTable(iptables.ProtocolIPv6),
	}
	Exposer = config
	logger.InfoLogger().Println("Gateway configured.")
	return config
}

func (gw *GatewayConfiguration) EnableServiceExposure(serviceID string, serviceIP net.IP, servicePort int, exposedPort int) error {
	if serviceIP.To4() != nil {
		err := gw.iptable4.AppendUnique("nat", "PREROUTING",
			"-p", "tcp", // tcp only
			"-d", gw.publicIPv4.String(), // destination gateway public IP
			"--dport", strconv.Itoa(exposedPort), // to the exposed service port
			"-j", "DNAT",
			"--to-destination", fmt.Sprintf("%s:%d", serviceIP.String(), servicePort)) // change destination to serviceIP:servicePort
		if err != nil {
			logger.ErrorLogger().Println("Error in PREROUTING iptable4 rule.")
			return err
		}
		err = gw.iptable4.AppendUnique("nat", "POSTROUTING",
			"-p", "tcp",
			"-d", serviceIP.String(),
			"--dport", strconv.Itoa(servicePort),
			"-j", "SNAT",
			"--to-source", gw.oakestraIPv4.String()) // change source to oakestra internal gateway IP
		if err != nil {
			logger.ErrorLogger().Println("Error in POSTROUTING iptable4 rule.")
			return err
		}
	} else if serviceIP.To16() != nil {
		err := gw.iptable6.AppendUnique("nat", "PREROUTING",
			"-p", "tcp",
			"-d", gw.publicIPv6.String(),
			"--dport", strconv.Itoa(exposedPort),
			"--to-destination", fmt.Sprintf("[%s]:%d", serviceIP.String(), servicePort))
		if err != nil {
			logger.ErrorLogger().Println("Error in PREROUTING iptable6 rule.")
			return err
		}
		err = gw.iptable6.AppendUnique("nat", "POSTROUTING",
			"-p", "tcp",
			"-d", serviceIP.String(),
			"--dport", strconv.Itoa(servicePort),
			"-j", "SNAT",
			"--to-source", gw.oakestraIPv6.String())
		if err != nil {
			logger.ErrorLogger().Println("Error in POSTROUTING iptable6 rule.")
			return err
		}
		// Forwarding to Service:
		// -A FORWARD -i <public_interface> -o <oakestra_interface> -p tcp --syn --dport <servicePort> -m conntrack --ctstate NEW -j ACCEPT
		// -A FORWARD -i <public_interface> -o <oakestra_interface> -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
		// -A FORWARD -i <oakestra_interface> -o <public_interface> -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

		// -A PREROUTING -i <public_interface> -p tcp --dport < -j DNAT --to-destination 10.0.0.1
		/*iptables \
				-A PREROUTING    # Append a rule to the PREROUTING chain
		  		-t nat           # The PREROUTING chain is in the nat table
		  		-p tcp           # Apply this rules only to tcp packets
		  		-d 192.168.1.1   # and only if the destination IP is 192.168.1.1
		  		--dport 27017    # and only if the destination port is 27017
		  		-j DNAT          # Use the DNAT target
		  		--to-destination # Change the TCP and IP destination header
		     	10.0.0.2:1234 # to 10.0.0.2:1234
		*/
		// -A POSTROUTING -d 10.0.0.1 -o eth1 -p tcp --dport 80 -j SNAT --to-source 10.0.0.2
		// TODO
		//fw.iptable6.AppendUnique()
	}

	gw.exposedPorts[exposedPort] = true
	gw.exposedServices[serviceID] = ServiceEntry{
		serviceIP:   serviceIP,
		exposedPort: exposedPort,
		servicePort: servicePort,
	}
	return nil
}

func (gw *GatewayConfiguration) DisableServiceExposure(serviceID string) error {
	_, exists := gw.exposedServices[serviceID]
	if !exists {
		return fmt.Errorf("service does not exist")
	}
	logger.InfoLogger().Printf("Closing ports for service %s", serviceID)
	// TODO remove firewall rules

	delete(gw.exposedServices, serviceID)
	return nil
}

func (fw *FirewallConfiguration) EnableServiceExposure(serviceID string, serviceIP net.IP, servicePort int, exposedPort int) error {
	/*if serviceIP.To16() != nil {
		// TODO
		fw.iptable6.AppendUnique()
	}
	if serviceIP.To4() != nil {
		// TODO
		fw.iptable4.AppendUnique()
	}*/
	fw.exposedPorts[exposedPort] = true
	fw.exposedServices[serviceID] = ServiceEntry{
		serviceIP:   serviceIP,
		exposedPort: exposedPort,
		servicePort: servicePort,
	}
	return nil
}

func (fw *FirewallConfiguration) DisableServiceExposure(serviceID string) error {
	// TODO
	return nil
}

func newGatewayInterface(ipv4 net.IP, ipv6 net.IP) *water.Interface {
	config := water.Config{
		DeviceType: water.TAP,
	}
	config.Name = "oak0"
	ifce, err := water.New(config)
	if err != nil {
		logger.InfoLogger().Fatal(err)
	}
	if ipv4 != nil {
		logger.InfoLogger().Println("Setting internal IPv4 address " + ipv4.String() + "/16")
		cmd := exec.Command("ip", "addr", "add", ipv4.String()+"/16", "dev", ifce.Name())
		err = cmd.Run()
		if err != nil {
			logger.InfoLogger().Fatal(err)
		}
	}

	if ipv6 != nil {
		logger.InfoLogger().Println("Setting internal IPv6 address " + ipv6.String() + "/21")
		cmd := exec.Command("ip", "addr", "add", ipv6.String()+"/21", "dev", ifce.Name())
		err = cmd.Run()
		if err != nil {
			logger.InfoLogger().Fatal(err)
		}
	}

	cmd := exec.Command("ip", "link", "set", "dev", ifce.Name(), "up")
	err = cmd.Run()
	if err != nil {
		logger.InfoLogger().Fatal(err)
	}

	// disabling reverse path filtering
	logger.InfoLogger().Println("Disabling internal gatetway dev reverse path filtering")
	cmd = exec.Command("echo", "0", ">", "/proc/sys/net/ipv4/conf/"+ifce.Name()+"/rp_filter")
	err = cmd.Run()
	if err != nil {
		log.Printf("Error disabling internal gateway reverse path filtering: %s ", err.Error())
	}

	logger.InfoLogger().Println("Interface set up.")
	return ifce
}
