package handlers

import (
	"NetManager/env"
	"NetManager/logger"
	"NetManager/mqtt"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
)

type ContainerDeployTask struct {
	Pid            int    `json:"pid"`
	ServiceName    string `json:"serviceName"`
	Instancenumber int    `json:"instanceNumber"`
	PortMappings   string `json:"portMappings"`
	IsUnikernel    bool   `json:"isUnikernel,omitempty"`
	PublicAddr     string
	PublicPort     string
	Env            *env.Environment
	Writer         *http.ResponseWriter
	Finish         chan TaskReady
}

type TaskReady struct {
	IP  net.IP
	Err error
}

type deployTaskQueue struct {
	newTask chan *ContainerDeployTask
}

type DeployTaskQueue interface {
	NewTask(request *ContainerDeployTask)
}

var once sync.Once
var taskQueue deployTaskQueue

func NewDeployTaskQueue() DeployTaskQueue {

	once.Do(func() {
		taskQueue = deployTaskQueue{
			newTask: make(chan *ContainerDeployTask, 50),
		}
		go taskQueue.taskExecutor()
	})
	return &taskQueue
}

func (t *deployTaskQueue) NewTask(request *ContainerDeployTask) {
	t.newTask <- request
}

func (t *deployTaskQueue) taskExecutor() {
	for true {
		select {
		case task := <-t.newTask:
			//deploy the network stack in the container
			addr, err := deploymentHandler(task)
			if err != nil {
				logger.ErrorLogger().Println("[ERROR]: ", err)
			}
			task.Finish <- TaskReady{
				IP:  addr,
				Err: err,
			}
			//asynchronously update proxy tables
			updateInternalProxyDataStructures(task)
		}
	}
}

func deploymentHandler(requestStruct *ContainerDeployTask) (net.IP, error) {

	//get app full name
	appCompleteName := strings.Split(requestStruct.ServiceName, ".")
	if len(appCompleteName) != 4 {
		return nil, errors.New(fmt.Sprintf("Invalid app name: %s", appCompleteName))
	}

	//attach network to the container
	addr, err := requestStruct.Env.AttachNetworkToContainer(requestStruct.Pid, requestStruct.ServiceName, requestStruct.Instancenumber, requestStruct.PortMappings)
	if err != nil {
		logger.ErrorLogger().Println("[ERROR]:", err)
		return nil, err
	}

	//notify to net-component
	err = mqtt.NotifyDeploymentStatus(
		requestStruct.ServiceName,
		"DEPLOYED",
		requestStruct.Instancenumber,
		addr.String(),
		requestStruct.PublicAddr,
		requestStruct.PublicPort,
	)
	if err != nil {
		logger.ErrorLogger().Println("[ERROR]:", err)
		return nil, err
	}

	return addr, nil
}

func updateInternalProxyDataStructures(requestStruct *ContainerDeployTask) {
	//Update internal table entry if an interest has not been set already.
	//Otherwise, do nothing, the net will autonomously update.
	if !mqtt.MqttIsInterestRegistered(requestStruct.ServiceName) {
		requestStruct.Env.RefreshServiceTable(requestStruct.ServiceName)
		mqtt.MqttRegisterInterest(requestStruct.ServiceName, requestStruct.Env, requestStruct.Instancenumber)
	}
}

func deploymentHandlerUnikernel(requestStruct *ContainerDeployRequest) {
	writer := *requestStruct.Writer
	appCompleteName := strings.Split(requestStruct.ServiceName, ".")
	if len(appCompleteName) != 4 {
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	//Create Ns and bridge/tap to deploy the Unikernel
	name := fmt.Sprintf("%s.instance.%d", requestStruct.ServiceName, requestStruct.Instancenumber)
	addr, err := requestStruct.Env.CreateUnikernelNetwork(name, requestStruct.ServiceName, requestStruct.PortMappings)
	if err != nil {
		log.Println("Failed to Create Network for Unikernel environment: ", err)
		writer.WriteHeader(http.StatusBadRequest)
		return
	}
	//notify net-component
	mqtt.NotifyDeploymentStatus(
		requestStruct.ServiceName,
		"DEPLOYED",
		requestStruct.Instancenumber,
		addr.String(),
		requestStruct.PublicAddr,
		requestStruct.PublicPort,
	)
	//update internal table entry
	requestStruct.Env.RefreshServiceTable(requestStruct.ServiceName)

	mqtt.MqttRegisterInterest(requestStruct.ServiceName, requestStruct.Env)

	//answer the caller
	response := DeployResponse{
		ServiceName: requestStruct.ServiceName,
		NsAddress:   addr.String(),
	}

	log.Println("Response to /unikernel/deploy: ", response)

	writer.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(writer).Encode(response)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
	}

}
