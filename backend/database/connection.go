package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

type DatabaseConfig struct {
	Host            string
	Port            int
	User            string
	Password        string
	Database        string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
}

type DB struct {
	*sqlx.DB
	Redis *redis.Client
}

var (
	db    *DB
	ctx   = context.Background()
)

// Initialize initialise la connexion à la base de données PostgreSQL et Redis
func Initialize(dbConfig DatabaseConfig, redisConfig RedisConfig) (*DB, error) {
	// Connexion PostgreSQL
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		dbConfig.Host,
		dbConfig.Port,
		dbConfig.User,
		dbConfig.Password,
		dbConfig.Database,
		dbConfig.SSLMode,
	)

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("erreur lors de l'ouverture de la connexion PostgreSQL: %w", err)
	}

	// Configuration du pool de connexions PostgreSQL
	sqlDB.SetMaxOpenConns(dbConfig.MaxOpenConns)
	sqlDB.SetMaxIdleConns(dbConfig.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(dbConfig.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(dbConfig.ConnMaxIdleTime)

	// Test de la connexion PostgreSQL
	if err := sqlDB.Ping(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("impossible de se connecter à PostgreSQL: %w", err)
	}

	// Création de l'instance sqlx
	sqlxDB := sqlx.NewDb(sqlDB, "postgres")

	// Connexion Redis
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", redisConfig.Host, redisConfig.Port),
		Password: redisConfig.Password,
		DB:       redisConfig.DB,
	})

	// Test de la connexion Redis
	if err := rdb.Ping(ctx).Err(); err != nil {
		sqlxDB.Close()
		rdb.Close()
		return nil, fmt.Errorf("impossible de se connecter à Redis: %w", err)
	}

	db = &DB{
		DB:    sqlxDB,
		Redis: rdb,
	}

	log.Println("Connexion aux bases de données établie avec succès")
	return db, nil
}

// GetDB retourne l'instance de la base de données
func GetDB() *DB {
	return db
}

// RunMigrations exécute les migrations de base de données
func RunMigrations(databaseURL string, migrationsPath string) error {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return fmt.Errorf("erreur lors de l'ouverture de la connexion pour les migrations: %w", err)
	}
	defer db.Close()

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("erreur lors de la création du driver PostgreSQL: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", migrationsPath),
		"postgres",
		driver,
	)
	if err != nil {
		return fmt.Errorf("erreur lors de la création de l'instance migrate: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("erreur lors de l'exécution des migrations: %w", err)
	}

	log.Println("Migrations exécutées avec succès")
	return nil
}

// Close ferme les connexions aux bases de données
func Close() error {
	if db != nil {
		var err error
		if db.DB != nil {
			if closeErr := db.DB.Close(); closeErr != nil {
				err = fmt.Errorf("erreur lors de la fermeture de PostgreSQL: %w", closeErr)
			}
		}
		if db.Redis != nil {
			if closeErr := db.Redis.Close(); closeErr != nil {
				if err != nil {
					err = fmt.Errorf("%v; erreur lors de la fermeture de Redis: %w", err, closeErr)
				} else {
					err = fmt.Errorf("erreur lors de la fermeture de Redis: %w", closeErr)
				}
			}
		}
		return err
	}
	return nil
}

// Transaction exécute une fonction dans une transaction
func Transaction(fn func(*sqlx.Tx) error) error {
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("erreur lors du début de la transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		} else if err != nil {
			tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(tx)
	return err
}

// HealthCheck vérifie la santé des connexions aux bases de données
func HealthCheck() error {
	if db == nil {
		return fmt.Errorf("base de données non initialisée")
	}

	// Vérification PostgreSQL
	if err := db.DB.Ping(); err != nil {
		return fmt.Errorf("PostgreSQL non accessible: %w", err)
	}

	// Vérification Redis
	if err := db.Redis.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis non accessible: %w", err)
	}

	return nil
}

// GetStats retourne les statistiques de la base de données
func GetStats() sql.DBStats {
	if db != nil && db.DB != nil {
		return db.DB.Stats()
	}
	return sql.DBStats{}
}

// CacheSet stocke une valeur dans Redis avec une expiration
func CacheSet(key string, value interface{}, expiration time.Duration) error {
	if db == nil || db.Redis == nil {
		return fmt.Errorf("Redis non disponible")
	}
	return db.Redis.Set(ctx, key, value, expiration).Err()
}

// CacheGet récupère une valeur depuis Redis
func CacheGet(key string) (string, error) {
	if db == nil || db.Redis == nil {
		return "", fmt.Errorf("Redis non disponible")
	}
	return db.Redis.Get(ctx, key).Result()
}

// CacheDel supprime une clé de Redis
func CacheDel(key string) error {
	if db == nil || db.Redis == nil {
		return fmt.Errorf("Redis non disponible")
	}
	return db.Redis.Del(ctx, key).Err()
}

// CacheExists vérifie si une clé existe dans Redis
func CacheExists(key string) (bool, error) {
	if db == nil || db.Redis == nil {
		return false, fmt.Errorf("Redis non disponible")
	}
	result := db.Redis.Exists(ctx, key)
	return result.Val() > 0, result.Err()
}