package api

import (
	"net/http"

	"github.com/Scrin/yt-dlp-ui/internal/network"
	"github.com/gin-gonic/gin"
)

func handleGetNetwork(settings *network.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, settings.View())
	}
}

func bindNetwork(c *gin.Context, settings *network.Store) (network.Update, bool) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var update network.Update
	if err := c.ShouldBindJSON(&update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid network settings"})
		return update, false
	}
	if _, err := settings.Preview(update); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return update, false
	}
	return update, true
}

func handleSaveNetwork(settings *network.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		update, ok := bindNetwork(c, settings)
		if !ok {
			return
		}
		if err := settings.Save(update); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, settings.View())
	}
}

func handleTestNetwork(settings *network.Store) gin.HandlerFunc {
	// Tests do not use download workers and cannot create unbounded dialers.
	slots := make(chan struct{}, 3)
	return func(c *gin.Context) {
		update, ok := bindNetwork(c, settings)
		if !ok {
			return
		}
		cfg, err := settings.Preview(update)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "connection tests are busy; try again shortly"})
			return
		}
		c.JSON(http.StatusOK, network.Test(c.Request.Context(), cfg))
	}
}
