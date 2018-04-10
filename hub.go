package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/shahob/config"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

type Config struct {
	Mongo struct {
		Dsn        string `json:"dsn"`
		Base       string `json:"base"`
		Collection string `json:"collection"`
	} `json:"mongo"`
	Trello struct {
		Key            string `json:"key"`
		Token          string `json:"token"`
		ListTesting    string `json:"listTesting"`
		ListInProgress string `json:"listInProgress"`
		Api            string `json:"api"`
	} `json:"trello"`
	Gitlab struct {
		Token     string `json:"token"`
		ProjectId string `json:"projectId"`
		Api       string `json:"api"`
	} `json:"gitlab"`
}

type GitLabIssueResponse struct {
	Id int `json:"id"`
}

type TrelloCardResponse struct {
	Id string `json:"id"`
}

type Hub struct {
	TrelloId string
	GitlabId int
	State    string
}

type TrelloPayload struct {
	Action struct {
		Type string `json:"type"`
		Display struct {
			TranslationKey string `json:"translationKey"`
			Entities struct {
				Card struct {
					Text string `json:"text"`
					Id   string `json:"id"`
				} `json:"card"`
				ListAfter struct {
					Id string `json:"id"`
				} `json:"listAfter"`
			} `json:"entities"`
		} `json:"display"`
	} `json:"action"`
}

type GitlabPayload struct {
	ObjectAttributes struct {
		Action string `json:"action"`
		Id     int    `json:"id"`
	} `json:"object_attributes"`
}

// Create issue in gitlab project
func GitlabTaskCreate(payload *TrelloPayload, conf *Config, GitlabId chan int) {
	client := &http.Client{}
	apiPath := conf.Gitlab.Api + "/projects/" + conf.Gitlab.ProjectId + "/issues"

	form := url.Values{}
	form.Add("title", payload.Action.Display.Entities.Card.Text)

	req, err := http.NewRequest("POST", apiPath, bytes.NewBufferString(form.Encode()))
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Add("PRIVATE-TOKEN", conf.Gitlab.Token)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	bodyBytes, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		log.Fatal(err)
	}

	var data GitLabIssueResponse
	err3 := json.Unmarshal(bodyBytes, &data)
	if err3 != nil {
		log.Fatal(err3)
	}

	GitlabId <- data.Id
}

// Save id for map
func SaveIds(payload *TrelloPayload, hubCollection *mgo.Collection, GitlabId chan int) {
	gid := <-GitlabId

	err := hubCollection.Insert(&Hub{payload.Action.Display.Entities.Card.Id, gid, "open"})
	if err != nil {
		log.Fatal(err)
	}
}

// Set status
func SaveStatus(hubCollection *mgo.Collection, TrelloId chan string) {
	tid := <-TrelloId
	err := hubCollection.Update(bson.M{"trelloid": tid}, bson.M{"$set": bson.M{"status": "closed"}})
	if err != nil {
		log.Fatal(err)
	}
}

// Change card list
func TrelloCardUpdate(payload *GitlabPayload, conf *Config, hub *Hub, TrelloId chan string) {
	fmt.Println(payload, hub)
	client := &http.Client{}
	apiPath := conf.Trello.Api + "/cards/" + hub.TrelloId + "/"

	form := url.Values{}
	form.Add("key", conf.Trello.Key)
	form.Add("token", conf.Trello.Token)
	form.Add("idList", conf.Trello.ListTesting)

	req, err := http.NewRequest("PUT", apiPath, bytes.NewBufferString(form.Encode()))
	if err != nil {
		log.Fatal(err)
	}

	req.Header.Set("content-type", "application/x-www-form-urlencoded")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	bodyBytes, err2 := ioutil.ReadAll(resp.Body)
	if err2 != nil {
		log.Fatal(err)
	}

	var data TrelloCardResponse
	err3 := json.Unmarshal(bodyBytes, &data)
	if err3 != nil {
		log.Fatal(err3)
	}

	TrelloId <- data.Id
}

func main() {
	configPath := flag.String("c", "config.json", "Configuration file path")
	port := flag.String("p", "8080", "listen and serve port")
	mode := flag.String("mode", "debug", "Mode")
	flag.Parse()

	if *mode == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	conf := Config{}
	err := config.Load(*configPath, &conf)
	if err != nil {
		log.Fatal(err)
	}

	session, err := mgo.Dial(conf.Mongo.Dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	// Optional. Switch the session to a monotonic behavior.
	session.SetMode(mgo.Monotonic, true)
	hubCollection := session.DB(conf.Mongo.Base).C(conf.Mongo.Collection)
	router := gin.Default()

	// Main page
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "It works!")
	})

	// GitLab webhook handler
	router.POST("/gitlab", func(c *gin.Context) {
		var payload GitlabPayload
		if err := c.ShouldBindJSON(&payload); err == nil {
			if payload.ObjectAttributes.Action == "close" {
				result := Hub{}
				err = hubCollection.Find(bson.M{"gitlabid": payload.ObjectAttributes.Id}).One(&result)
				if err == nil {
					TrelloId := make(chan string)
					go TrelloCardUpdate(&payload, &conf, &result, TrelloId)
					go SaveStatus(hubCollection, TrelloId)
				} else {
					log.Fatal(err)
					c.Status(http.StatusInternalServerError)
				}
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// Trello webhook handler
	router.POST("/trello", func(c *gin.Context) {
		var payload TrelloPayload
		if err := c.ShouldBindJSON(&payload); err == nil {
			if payload.Action.Display.TranslationKey == "action_move_card_from_list_to_list" &&
				conf.Trello.ListInProgress == payload.Action.Display.Entities.ListAfter.Id {
				GitlabId := make(chan int)
				go GitlabTaskCreate(&payload, &conf, GitlabId)
				go SaveIds(&payload, hubCollection, GitlabId)
			}
		}

		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// Trello webhook make validation HEAD request
	// When you create a webhook, Trello will make a HEAD request to callbackURL you provide to verify that it is a valid URL. Failing to respond to the HEAD request will result in the webhook failing to be created.
	router.HEAD("/trello", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	// TODO
	serverPort := ":" + *port
	router.Run(serverPort)
}
