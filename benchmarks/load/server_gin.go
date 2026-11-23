//go:build !nogin

package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func init() { ginMaker = ginServer }

func ginServer() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New() // no logger or recovery, like the other two servers
	r.GET("/hello", func(c *gin.Context) { c.JSON(http.StatusOK, HelloOut{Message: "Hello"}) })
	r.POST("/users", func(c *gin.Context) {
		var in UserIn
		if err := c.ShouldBindJSON(&in); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusCreated, UserOut{ID: 1, Name: in.Name, Email: in.Email})
	})
	r.GET("/__mem", func(c *gin.Context) { memHandler(c.Writer, c.Request) })
	return r
}
