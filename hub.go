package main

import (
	"flag"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg"
	"github.com/shahob/config"
)

func main() {
	var configPath = flag.String("c", "config.json", "Configuration file path")
	flag.Parse()

	// get configuration file
	conf := config.Load(configPath)

	router := gin.Default()

	db := pg.Connect(&pg.Options{
		User:     conf.Database.User,
		Password: conf.Database.Password,
		Database: conf.Database.Base,
	})
	defer db.Close()

	// Main page
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "HUB")
	})

	router.HEAD("/", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status": "ok",
		})
	})

	router.Run()
}
