package main

import (
	"NetManager/env"
	"NetManager/gateway"
	"NetManager/handlers"
	"NetManager/logger"
	"NetManager/mqtt"
	"NetManager/network"
	"NetManager/playground"
	"NetManager/proxy"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/tkanos/gonfig"
)

type undeployRequest struct {
	Servicename    string `json:"serviceName"`
	Instancenumber int    `json:"instanceNumber"`
}

type registerRequest struct {
	ClientID string `json:"client_id"`
}

type DeployResponse struct {
	ServiceName string `json:"serviceName"`
	NsAddress   string `json:"nsAddress"`
}

type netConfiguration struct {
	NodePublicAddress string
	NodePublicPort    string
	ClusterUrl        string
	ClusterMqttPort   string
}

func handleRequests(port int) {
	netRouter := mux.NewRouter().StrictSlash(true)
	netRouter.HandleFunc("/register", register).Methods("POST")

	handlers.RegisterAllManagers(
		&Env,
		&WorkerID,
		Configuration.NodePublicAddress,
		Configuration.NodePublicPort,
		netRouter,
	)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", port), netRouter))
}

var (
	Env env.Environment
	// Proxy         proxy.GoProxyTunnel
	WorkerID      string
	NetManagerID  string
	Configuration netConfiguration
)

/*
Endpoint: /register
Usage: used to initialize the Network manager. The network manager must know his local subnetwork.
Method: POST
Request Json:

	{
		client_id:string # id of the worker node
	}

Response: 200 or Failure code
*/
func register(writer http.ResponseWriter, request *http.Request) {
	log.Println("Received HTTP request - /register ")

	reqBody, _ := io.ReadAll(request.Body)
	var requestStruct registerRequest
	err := json.Unmarshal(reqBody, &requestStruct)
	if err != nil {
		writer.WriteHeader(http.StatusBadRequest)
	}
	log.Println(requestStruct)

	// drop the request if the node is already initialized
	if WorkerID != "" {
		if WorkerID == requestStruct.ClientID {
			log.Printf("Node already initialized")
			writer.WriteHeader(http.StatusOK)
		} else {
			log.Printf("Attempting to re-initialize a node with a different worker ID")
			writer.WriteHeader(http.StatusBadRequest)
		}
		return
	}

	WorkerID = requestStruct.ClientID

	// initialize mqtt connection to the broker
	// mqtt.InitNetMqttClient(requestStruct.ClientID, Configuration.ClusterUrl, Configuration.ClusterMqttPort)
	mqtt.GetNetMqttClient().RegisterWorker(WorkerID)

	writer.WriteHeader(http.StatusOK)
}

func main() {
	cfgFile := flag.String("cfg", "/etc/netmanager/netcfg.json", "Set a cluster IP")
	localPort := flag.Int("p", 6000, "Default local port of the NetManager")
	debugMode := flag.Bool("D", false, "Debug mode, it enables debug-level logs")
	p2pMode := flag.Bool(
		"p2p",
		false,
		"Start the engine in p2p mode (playground2playground), requires the address of a peer node. Useful for debugging.",
	)
	flag.Parse()

	err := gonfig.GetConf(*cfgFile, &Configuration)
	if err != nil {
		log.Fatal(err)
	}

	if *debugMode {
		logger.SetDebugMode()
	}

	log.Println("Registering to Cluster...")
	log.Println("Contacting Cluster: ", Configuration.ClusterUrl)
	// get initial ID and init MQTT client
	NetManagerID = gateway.RegisterNetmanager(
		Configuration.ClusterUrl,
		Configuration.NodePublicPort,
	)
	log.Println("Registered: ", NetManagerID)

	mqtt.InitNetMqttClient(NetManagerID, Configuration.ClusterUrl, Configuration.ClusterMqttPort)
	mqtt.GetNetMqttClient().
		RegisterTopic(fmt.Sprintf("nodes/%s/net/gateway/deploy", mqtt.GetNetMqttClient().ClientID), gateway.GatewayDeploymentHandler)

	// Moved interface setup into main, since we can assume that a netmanager is going to be used at some point regardless
	// of whether as a gateway or worker node - both require the network stack to work
	// initialize the proxy tunnel
	// Proxy = proxy.New()
	// Proxy.Listen()
	proxy.New()
	proxy.Proxy().Listen()

	// initialize the Env Manager
	Env = *env.NewEnvironmentClusterConfigured(proxy.Proxy().HostTUNDeviceName)

	// Proxy.SetEnvironment(&Env)
	proxy.Proxy().SetEnvironment(&Env)

	log.Print(Configuration)
	log.Print(mqtt.GetNetMqttClient())
	network.IptableFlushAll()

	if *p2pMode {
		defer playground.APP.Stop()
		playground.CliLoop(Configuration.NodePublicAddress, Configuration.NodePublicPort)
	}

	log.Println("NetManager started. Waiting for worker registration.")
	handleRequests(*localPort)
}
