#!/bin/bash

set -e

echo "🧪 Lancement des tests StackShip"

# Tests backend
echo "🔧 Tests backend..."
cd backend
go test ./... -v

# Tests frontend
echo "🎨 Tests frontend..."
cd ../frontend
npm test

# Tests E2E (optionnel)
if [ "$1" = "e2e" ]; then
    echo "🌐 Tests E2E..."
    npm run cypress:run
fi

echo "✅ Tous les tests sont passés!"
