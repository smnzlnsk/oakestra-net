package mqtt

import (
	"NetManager/events"
	"NetManager/logger"
	"NetManager/utils"
	"encoding/json"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

var (
	runningHandlers     = utils.NewStringSlice()
	runningHandlersLock sync.RWMutex
)

type jobUpdatesTimer struct {
	eventManager events.EventManager
	env          jobEnvironmentManagerActions
	client       *NetMqttClient
	job          string
	topic        string
	instance     int
}

type jobEnvironmentManagerActions interface {
	RefreshServiceTable(sname string)
	RemoveServiceEntries(sname string)
	IsServiceDeployed(fullSnameAndInstance string) bool
}

type mqttInterestDeregisterRequest struct {
	Appname string `json:"appname"`
}

func (jut *jobUpdatesTimer) MessageHandler(client mqtt.Client, message mqtt.Message) {
	logger.InfoLogger().Printf("Received job update regarding %s", message.Topic())
	go jut.env.RefreshServiceTable(jut.job)
}

func (jut *jobUpdatesTimer) startSelfDestructTimeout() {
	/*
		If any worker still requires this job, reset timer. If in 5 minutes nobody needs this service, de-register the interest.
	*/
	logger.InfoLogger().Printf("self destruction timeout started for job %s", jut.job)
	eventManager := events.GetInstance()
	eventChan, _ := eventManager.Register(events.TableQuery, jut.job)
	for {
		select {
		case <-eventChan:
			// event received, reset timer
			logger.DebugLogger().Printf("received packet event from: %s", jut.job)
			continue
		case <-time.After(10 * time.Second):
			if !jut.env.IsServiceDeployed(jut.job) {
				// timeout ----> job no longer required. Let's clear the interest
				log.Printf("De-registering from %s", jut.job)
				cleanInterestTowardsJob(jut.job)
				jut.client.DeRegisterTopic(jut.topic)
				runningHandlersLock.Lock()
				runningHandlers.RemoveElem(jut.job)
				runningHandlersLock.Unlock()
				eventManager.DeRegister(events.TableQuery, jut.job)
				jut.env.RemoveServiceEntries(jut.job)
				return
			}
			continue
		}
	}
}

// MqttRegisterInterest :
/* Register an interest in a route for 5 minutes.
If the route is not used for more than 5 minutes the interest is removed
If the instance number is provided, the interest is kept until that instance is deployed in the node */
func MqttRegisterInterest(jobName string, env jobEnvironmentManagerActions, instance ...int) {
	runningHandlersLock.Lock()
	defer runningHandlersLock.Unlock()
	if runningHandlers.Exists(jobName) {
		logger.InfoLogger().Printf("Interest for job %s already registered", jobName)
		return
	}

	instanceNumber := -1
	if len(instance) > 0 {
		instanceNumber = instance[0]
	}

	jobTimer := jobUpdatesTimer{
		eventManager: events.GetInstance(),
		job:          jobName,
		env:          env,
		client:       GetNetMqttClient(),
		instance:     instanceNumber,
	}

	jobTimer.topic = "jobs/" + jobName + "/updates_available"
	GetNetMqttClient().RegisterTopic(jobTimer.topic, jobTimer.MessageHandler)
	logger.InfoLogger().Printf("MQTT - Subscribed to %s ", jobTimer.topic)
	runningHandlers.Add(jobTimer.job)
	go jobTimer.startSelfDestructTimeout()
}

func MqttIsInterestRegistered(jobName string) bool {
	runningHandlersLock.RLock()
	defer runningHandlersLock.RUnlock()
	return runningHandlers.Exists(jobName)
}

func cleanInterestTowardsJob(jobName string) {
	request := mqttInterestDeregisterRequest{Appname: jobName}
	jsonreq, _ := json.Marshal(request)
	_ = GetNetMqttClient().PublishToBroker("interest/remove", string(jsonreq))
}
