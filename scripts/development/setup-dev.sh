#!/bin/bash

set -e

echo "🚀 Configuration de l'environnement de développement StackShip"

# Vérifier les prérequis
echo "✅ Vérification des prérequis..."
command -v go >/dev/null 2>&1 || { echo "❌ Go n'est pas installé"; exit 1; }
command -v node >/dev/null 2>&1 || { echo "❌ Node.js n'est pas installé"; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "❌ Docker n'est pas installé"; exit 1; }

# Installation des dépendances backend
echo "📦 Installation des dépendances backend..."
cd backend
go mod tidy

# Installation des dépendances frontend
echo "📦 Installation des dépendances frontend..."
cd ../frontend
npm install

# Démarrage des services
echo "🐳 Démarrage des services Docker..."
cd ..
docker-compose up -d postgres redis

# Attendre que les services soient prêts
echo "⏳ Attente du démarrage des services..."
sleep 10

# Créer les tables de base
echo "🗃️ Création des tables de base de données..."
cd backend
go run main.go migrate

echo "✅ Environnement de développement configuré avec succès!"
echo "🎯 Vous pouvez maintenant lancer 'make dev' pour démarrer l'application"
