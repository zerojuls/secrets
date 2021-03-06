package rest

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/didip/tollbooth"
	"github.com/gin-gonic/gin"
	"github.com/umputun/secrets/app/messager"
)

const limitReqSec = 5

// Server is a rest with store
type Server struct {
	Messager       *messager.MessageProc
	PinSize        int
	MaxPinAttempts int
	MaxExpSecs     int
}

//Run the lister and request's router, activate rest server
func (s Server) Run() {
	log.Printf("[INFO] activate rest server")

	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(s.limiterMiddleware())
	router.Use(s.loggerMiddleware())

	v1 := router.Group("/v1")
	{
		v1.POST("/message", s.saveMessageCtrl)
		v1.GET("/message/:key/:pin", s.getMessageCtrl)
		v1.GET("/params", s.getParamsCtrl)
		v1.GET("/ping", func(c *gin.Context) { c.String(200, "pong") })
	}

	log.Fatal(router.Run(":8080"))
}

// POST /v1/message
func (s Server) saveMessageCtrl(c *gin.Context) {
	request := struct {
		Message string `binding:"required"`
		Exp     int    `binding:"required"`
		Pin     string `binding:"required"`
	}{}

	err := c.BindJSON(&request)
	if err != nil {
		log.Printf("[WARN] can't bind request %v", request)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if len(request.Pin) != s.PinSize {
		log.Printf("[WARN] incorrect pin size %d", len(request.Pin))
		c.JSON(http.StatusBadRequest, gin.H{"error": "Incorrect pin size"})
		return
	}

	c.Set("post", fmt.Sprintf("msg: *****, pin: *****, exp: %v %v",
		time.Second*time.Duration(request.Exp), time.Now().Add(time.Second*time.Duration(request.Exp)).Format("2006/01/02-15:04:05")))

	r, err := s.Messager.MakeMessage(time.Second*time.Duration(request.Exp), request.Message, request.Pin)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"key": r.Key, "exp": r.Exp})
}

// GET /v1/message/:key/:pin
func (s Server) getMessageCtrl(c *gin.Context) {

	key, pin := c.Param("key"), c.Param("pin")
	if key == "" || pin == "" || len(pin) != s.PinSize {
		log.Print("[WARN] no valid key or pin in get request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "no key or pin passed"})
		return
	}

	serveRequest := func() (status int, res gin.H) {
		r, err := s.Messager.LoadMessage(key, pin)
		if err != nil {
			log.Printf("[WARN] failed to load key %v", key)
			if err == messager.ErrBadPinAttempt {
				return http.StatusExpectationFailed, gin.H{"error": err.Error()}
			}
			return http.StatusBadRequest, gin.H{"error": err.Error()}
		}
		return http.StatusOK, gin.H{"key": r.Key, "message": r.Data}
	}

	//make sure serveRequest works constant time on any branch
	st := time.Now()
	status, res := serveRequest()
	time.Sleep(time.Millisecond*250 - time.Since(st))
	c.JSON(status, res)
}

// GET /params
func (s Server) getParamsCtrl(c *gin.Context) {
	params := struct {
		PinSize        int `json:"pin_size"`
		MaxPinAttempts int `json:"max_pin_attempts"`
		MaxExpSecs     int `json:"max_exp_sec"`
	}{}

	params.PinSize = s.PinSize
	params.MaxPinAttempts = s.MaxPinAttempts
	params.MaxExpSecs = s.MaxExpSecs
	c.JSON(http.StatusOK, params)
}

func (s Server) limiterMiddleware() gin.HandlerFunc {
	limiter := tollbooth.NewLimiter(limitReqSec, time.Second)
	return func(c *gin.Context) {
		keys := []string{c.ClientIP(), c.Request.Header.Get("User-Agent")}
		if httpError := tollbooth.LimitByKeys(limiter, keys); httpError != nil {
			c.JSON(httpError.StatusCode, gin.H{"error": httpError.Message})
			c.Abort()
		} else {
			c.Next()
		}
	}
}

func (s Server) loggerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		t := time.Now()
		c.Next()

		body := ""
		if b, ok := c.Get("post"); ok {
			body = fmt.Sprintf("%v", b)
		}

		reqPath := c.Request.URL.Path
		if strings.HasPrefix(reqPath, "/v1/message/") {
			reqPath = "/v1/message/*****/*****"
		}
		log.Printf("[INFO] %s %s {%s} %s %v %d",
			c.Request.Method, reqPath, body,
			c.ClientIP(), time.Since(t), c.Writer.Status())

	}
}
