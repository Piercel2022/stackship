
# 🚀 StackShip - Plateforme DevOps

Une plateforme complète de déploiement et de monitoring pour applications containerisées.

## 🌟 Fonctionnalités

- **Gestion de projets** : Interface intuitive pour gérer vos applications
- **Déploiement automatisé** : Pipeline CI/CD intégré avec Docker et Kubernetes
- **Monitoring temps réel** : Métriques, logs et alertes avec Prometheus/Grafana
- **Sécurité** : Authentification JWT, RBAC et secrets management
- **Scalabilité** : Architecture microservices prête pour la production

## 🛠️ Stack Technique

### Backend
- **Go** avec Gin framework
- **PostgreSQL** pour la persistance
- **Redis** pour le cache
- **WebSocket** pour les mises à jour temps réel

### Frontend
- **React** avec TypeScript
- **Tailwind CSS** pour le styling
- **Redux** pour la gestion d'état
- **React Query** pour la gestion des données

### Infrastructure
- **Docker** & **Docker Compose**
- **Kubernetes** pour l'orchestration
- **Terraform** pour l'infrastructure as code
- **Helm** pour le packaging

### Monitoring
- **Prometheus** pour les métriques
- **Grafana** pour la visualisation
- **Loki** pour les logs centralisés

## 🚀 Démarrage rapide

### Prérequis
- Go 1.21+
- Node.js 18+
- Docker & Docker Compose
- Git

### Installation

1. **Cloner le repository**
```bash
git clone <repository-url>
cd stackship
```

2. **Initialiser le projet**
```bash
chmod +x init-project.sh
./init-project.sh
```

3. **Configurer l'environnement**
```bash
cp .env.example .env
# Éditer .env avec vos valeurs
```

4. **Démarrer en développement**
```bash
# Démarrer les services
./scripts/development/setup-dev.sh

# Terminal 1 - Backend
cd backend
go run main.go

# Terminal 2 - Frontend  
cd frontend
npm run dev
```

5. **Accéder à l'application**
- Frontend: http://localhost:3000
- Backend API: http://localhost:8080
- Documentation API: http://localhost:8080/swagger

## 🧪 Tests

```bash
# Tous les tests
./scripts/development/run-tests.sh

# Tests backend uniquement
cd backend && go test ./...

# Tests frontend uniquement
cd frontend && npm test
```

## 🚀 Déploiement

### Développement
```bash
docker-compose up -d
```

### Production
```bash
./scripts/deployment/deploy.sh
```

### Kubernetes
```bash
# Appliquer les manifests
kubectl apply -f infrastructure/kubernetes/

# Ou utiliser Helm
helm install stackship infrastructure/helm/stackship/
```

## 📊 Monitoring

Accès aux dashboards :
- **Grafana**: http://localhost:3001 (admin/admin)
- **Prometheus**: http://localhost:9090

## 🔧 Configuration

Les fichiers de configuration se trouvent dans `config/environments/` :
- `development.yaml` - Environnement de développement
- `staging.yaml` - Environnement de staging
- `production.yaml` - Environnement de production

## 🤝 Contribution

1. Fork le projet
2. Créer une branche feature (`git checkout -b feature/AmazingFeature`)
3. Commit les changements (`git commit -m 'Add some AmazingFeature'`)
4. Push vers la branche (`git push origin feature/AmazingFeature`)
5. Ouvrir une Pull Request

## 📝 License

Ce projet est sous license MIT. Voir le fichier `LICENSE` pour plus de détails.

## 🆘 Support

Pour toute question ou problème :
- Ouvrir une issue sur GitHub
- Consulter la documentation dans `/docs`
- Rejoindre notre Discord communautaire

Développé avec ❤️ pour simplifier le DevOps