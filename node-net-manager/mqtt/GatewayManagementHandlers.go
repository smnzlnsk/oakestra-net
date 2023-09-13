package mqtt

import (
	"NetManager/logger"
	"NetManager/model"
	"NetManager/network"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type mqttFirewallRequest struct {
	Firewall_ID   string `json:"firewall_id"`
	Public_IPv4   net.IP `json:"public_ipv4"`
	Public_IPv6   net.IP `json:"public_ipv6"`
	Oakestra_IPv4 net.IP `json:"oakestra_ip"`
}

type mqttGatewayRequest struct {
	Gateway_ID    string `json:"gateway_id"`
	Public_IPv4   net.IP `json:"public_ipv4"`
	Public_IPv6   net.IP `json:"public_ipv6"`
	Oakestra_IPv4 net.IP `json:"oakestra_ipv4"`
	Oakestra_IPv6 net.IP `json:"oakestra_ipv6"`
}

type mqttFirewallUpdate struct {
	FirewallID    string `json:"firewall_id"`
	Method        string `json:"method"`
	ServiceID     string `json:"service_id"`
	ServiceIP     net.IP `json:"service_ip"`
	Internal_Port int    `json:"internal_port"`
	Exposed_Port  int    `json:"exposed_port"`
}

type helloAnswer struct {
	ID string `json:"id"`
}

var firewall network.FirewallConfiguration

func RegisterNetmanager(address string, mqttPort string) string {
	jsondata, err := json.Marshal(model.GetNodeInfo())
	if err != nil {
		logger.ErrorLogger().Fatalf("Marshaling of node information failed, %v", err)
	}
	jsonbody := bytes.NewBuffer(jsondata)
	resp, err := http.Post(fmt.Sprintf("http://%s:10100/api/netmanager/register", address), "application/json", jsonbody)
	if err != nil {
		logger.ErrorLogger().Fatalf("Handshake failed, %v", err)
	}
	if resp.StatusCode != 200 {
		logger.ErrorLogger().Fatalf("Handshake failed with error code %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	ans := helloAnswer{}
	responseBytes, _ := io.ReadAll(resp.Body)
	err = json.Unmarshal(responseBytes, &ans)
	if err != nil {
		logger.ErrorLogger().Fatalf("Handshake failed, %v", err)
	}
	InitNetMqttClient(ans.ID, address, mqttPort)
	return ans.ID
}

func FirewallDeploymentHandler(client mqtt.Client, msg mqtt.Message) {
	log.Printf("MQTT - Received mqtt firewall deployment message: %s", msg.Payload())

	payload := msg.Payload()
	var req mqttFirewallRequest
	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Println(err)
	}

	network.StartFirewallProcess(req.Firewall_ID, req.Public_IPv4, req.Public_IPv6)
	GetNetMqttClient().RegisterTopic(fmt.Sprintf("nodes/%s/net/firewall/update", req.Firewall_ID), FirewallUpdateHandler)
	/*
		jsonres, _ := json.Marshal(req)
		return GetNetMqttClient().PublishToBroker("firewall/deployed", string(jsonres))
	*/
}

func FirewallUpdateHandler(client mqtt.Client, msg mqtt.Message) {
	log.Printf("MQTT - Received mqtt firewall update message: %s", msg.Payload())

	payload := msg.Payload()
	var req mqttFirewallUpdate
	err := json.Unmarshal(payload, &req)
	if err != nil {
		log.Println(err)
	}
	switch req.Method {
	case "POST":
		err := firewall.EnableServiceExposure(req.ServiceID, req.ServiceIP, req.Internal_Port, req.Exposed_Port)
		if err != nil {
			logger.ErrorLogger().Printf("Failed to enable firewall for %s", req.ServiceID)
		}
	case "DELETE":
		err := firewall.DisableServiceExposure(req.ServiceID)
		if err != nil {
			logger.ErrorLogger().Printf("Failed to disable firewall for %s", req.ServiceID)
		}
	default:
		logger.ErrorLogger().Println("Unknown Firewall Update Method. Dropping request.")
	}
}

func GatewayDeploymentHandler(client mqtt.Client, msg mqtt.Message) {
	logger.InfoLogger().Printf("MQTT - Received mqtt gateway deploy message: %s", msg.Payload())

	payload := msg.Payload()
	var req mqttGatewayRequest
	err := json.Unmarshal(payload, &req)
	if err != nil {
		logger.InfoLogger().Println(err)
	}
	network.StartGatewayProcess(req.Gateway_ID, req.Public_IPv4, req.Public_IPv6, req.Oakestra_IPv4, req.Oakestra_IPv6)
}
