package main

import (
    "log"
    "os"
    "stackship/config"
    "stackship/database"
    "stackship/api/auth"
    "stackship/api/projects"
    "stackship/api/deployments"
    "stackship/api/monitoring"
    "stackship/api/websocket"
    "stackship/utils"
    
    "github.com/gin-gonic/gin"
    "github.com/gin-contrib/cors"
)

func main() {
    // Initialiser la configuration
    cfg := config.LoadConfig()
    
    // Initialiser le logger
    logger := utils.NewLogger()
    
    // Initialiser la base de données
    db, err := database.Connect(cfg.Database)
    if err != nil {
        log.Fatal("Erreur de connexion à la base de données:", err)
    }
    
    // Initialiser le router Gin
    r := gin.Default()
    
    // Middleware CORS
    r.Use(cors.New(cors.Config{
        AllowOrigins:     []string{"http://localhost:3000", "http://localhost:5173"},
        AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
        AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
        ExposeHeaders:    []string{"Content-Length"},
        AllowCredentials: true,
    }))
    
    // Middleware d'authentification
    authMiddleware := auth.NewAuthMiddleware(cfg.JWT.Secret)
    
    // Routes API
    api := r.Group("/api/v1")
    {
        // Routes d'authentification
        auth.RegisterRoutes(api, db, cfg)
        
        // Routes protégées
        protected := api.Group("/")
        protected.Use(authMiddleware.AuthRequired())
        {
            projects.RegisterRoutes(protected, db, logger)
            deployments.RegisterRoutes(protected, db, logger)
            monitoring.RegisterRoutes(protected, db, logger)
        }
    }
    
    // WebSocket Hub
    hub := websocket.NewHub()
    go hub.Run()
    
    // Route WebSocket
    r.GET("/ws", func(c *gin.Context) {
        websocket.HandleWebSocket(hub, c)
    })
    
    // Démarrer le serveur
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }
    
    logger.Info("Serveur StackShip démarré sur le port " + port)
    log.Fatal(r.Run(":" + port))
}