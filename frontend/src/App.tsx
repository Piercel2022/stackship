import React from 'react'
import { BrowserRouter as Router, Routes, Route } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from 'react-query'
import { LoginForm } from './components/auth/LoginForm'
import { Dashboard } from './components/dashboard/Overview'
import { ProtectedRoute } from './components/auth/ProtectedRoute'
import { Header } from './components/common/Header'
import { Sidebar } from './components/common/Sidebar'
import './index.css'

const queryClient = new QueryClient()

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <Router>
        <div className="min-h-screen bg-gray-50">
          <Routes>
            <Route path="/login" element={<LoginForm />} />
            <Route path="/*" element={
              <ProtectedRoute>
                <div className="flex h-screen">
                  <Sidebar />
                  <div className="flex-1 flex flex-col">
                    <Header />
                    <main className="flex-1 overflow-y-auto p-6">
                      <Routes>
                        <Route path="/" element={<Dashboard />} />
                        <Route path="/projects" element={<div>Projects</div>} />
                        <Route path="/deployments" element={<div>Deployments</div>} />
                        <Route path="/monitoring" element={<div>Monitoring</div>} />
                        <Route path="/settings" element={<div>Settings</div>} />
                      </Routes>
                    </main>
                  </div>
                </div>
              </ProtectedRoute>
            } />
          </Routes>
        </div>
      </Router>
    </QueryClientProvider>
  )
}

export default App