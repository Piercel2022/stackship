-- StackShip Initial Database Schema
-- This migration creates the foundational tables for the StackShip platform

-- Enable UUID extension for generating unique identifiers
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Enable pg_trgm extension for full-text search capabilities
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Create custom types for better data integrity
CREATE TYPE user_role AS ENUM ('admin', 'developer', 'viewer');
CREATE TYPE user_status AS ENUM ('active', 'inactive', 'pending', 'suspended');
CREATE TYPE project_status AS ENUM ('active', 'archived', 'paused');
CREATE TYPE deployment_status AS ENUM ('pending', 'building', 'deploying', 'running', 'failed', 'stopped');
CREATE TYPE deployment_environment AS ENUM ('development', 'staging', 'production');
CREATE TYPE notification_type AS ENUM ('email', 'slack', 'discord', 'webhook');
CREATE TYPE notification_status AS ENUM ('pending', 'sent', 'failed');
CREATE TYPE log_level AS ENUM ('debug', 'info', 'warning', 'error', 'critical');

-- Users table for authentication and user management
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    email VARCHAR(255) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    username VARCHAR(100) UNIQUE NOT NULL,
    first_name VARCHAR(100),
    last_name VARCHAR(100),
    role user_role NOT NULL DEFAULT 'developer',
    status user_status NOT NULL DEFAULT 'active',
    avatar_url TEXT,
    two_factor_enabled BOOLEAN DEFAULT FALSE,
    two_factor_secret VARCHAR(255),
    last_login_at TIMESTAMP WITH TIME ZONE,
    email_verified BOOLEAN DEFAULT FALSE,
    email_verification_token VARCHAR(255),
    password_reset_token VARCHAR(255),
    password_reset_expires_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Teams table for organizing users into groups
CREATE TABLE teams (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(100) NOT NULL,
    description TEXT,
    slug VARCHAR(100) UNIQUE NOT NULL,
    owner_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Team memberships junction table
CREATE TABLE team_memberships (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role user_role NOT NULL DEFAULT 'developer',
    joined_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(team_id, user_id)
);

-- Projects table for managing application projects
CREATE TABLE projects (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    slug VARCHAR(255) UNIQUE NOT NULL,
    status project_status NOT NULL DEFAULT 'active',
    team_id UUID NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    created_by UUID NOT NULL REFERENCES users(id),
    
    -- Git repository information
    git_repository_url TEXT,
    git_branch VARCHAR(255) DEFAULT 'main',
    git_webhook_secret VARCHAR(255),
    
    -- Docker configuration
    dockerfile_path VARCHAR(255) DEFAULT 'Dockerfile',
    docker_context_path VARCHAR(255) DEFAULT '.',
    docker_registry_url TEXT,
    docker_image_name VARCHAR(255),
    
    -- Kubernetes configuration
    kubernetes_namespace VARCHAR(255),
    kubernetes_config JSONB,
    
    -- Environment variables and secrets
    environment_variables JSONB DEFAULT '{}',
    secrets JSONB DEFAULT '{}',
    
    -- Build and deployment settings
    build_command TEXT,
    install_command TEXT,
    start_command TEXT,
    health_check_path VARCHAR(255) DEFAULT '/health',
    port INTEGER DEFAULT 8080,
    
    -- Monitoring and alerting
    monitoring_enabled BOOLEAN DEFAULT TRUE,
    alerting_enabled BOOLEAN DEFAULT TRUE,
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Deployments table for tracking deployment history and status
CREATE TABLE deployments (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    environment deployment_environment NOT NULL,
    status deployment_status NOT NULL DEFAULT 'pending',
    
    -- Deployment metadata
    version VARCHAR(100),
    git_commit_hash VARCHAR(40),
    git_commit_message TEXT,
    git_branch VARCHAR(255),
    git_author_name VARCHAR(255),
    git_author_email VARCHAR(255),
    
    -- Build information
    build_id VARCHAR(255),
    build_logs TEXT,
    docker_image_tag VARCHAR(255),
    
    -- Kubernetes deployment details
    kubernetes_deployment_name VARCHAR(255),
    kubernetes_service_name VARCHAR(255),
    kubernetes_ingress_name VARCHAR(255),
    kubernetes_manifest JSONB,
    
    -- Deployment timing
    started_at TIMESTAMP WITH TIME ZONE,
    finished_at TIMESTAMP WITH TIME ZONE,
    duration_seconds INTEGER,
    
    -- Resource allocation
    cpu_request VARCHAR(50),
    memory_request VARCHAR(50),
    cpu_limit VARCHAR(50),
    memory_limit VARCHAR(50),
    replica_count INTEGER DEFAULT 1,
    
    -- Health and monitoring
    health_check_url TEXT,
    monitoring_url TEXT,
    logs_url TEXT,
    
    -- Rollback information
    rollback_target_id UUID REFERENCES deployments(id),
    is_rollback BOOLEAN DEFAULT FALSE,
    
    -- Metadata
    deployed_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Deployment logs table for storing build and runtime logs
CREATE TABLE deployment_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    log_level log_level NOT NULL DEFAULT 'info',
    message TEXT NOT NULL,
    source VARCHAR(100), -- e.g., 'build', 'deploy', 'runtime'
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    metadata JSONB DEFAULT '{}'
);

-- Metrics table for storing application and system metrics
CREATE TABLE metrics (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    deployment_id UUID REFERENCES deployments(id) ON DELETE CASCADE,
    metric_name VARCHAR(255) NOT NULL,
    metric_value NUMERIC NOT NULL,
    metric_type VARCHAR(50) NOT NULL, -- counter, gauge, histogram, summary
    labels JSONB DEFAULT '{}',
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    -- Index for time-series queries
    INDEX idx_metrics_timestamp (timestamp),
    INDEX idx_metrics_project_name (project_id, metric_name),
    INDEX idx_metrics_deployment (deployment_id)
);

-- Notifications table for managing alerts and notifications
CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    type notification_type NOT NULL,
    status notification_status NOT NULL DEFAULT 'pending',
    
    -- Notification content
    subject VARCHAR(255),
    message TEXT NOT NULL,
    priority VARCHAR(20) DEFAULT 'normal', -- low, normal, high, critical
    
    -- Targeting
    project_id UUID REFERENCES projects(id) ON DELETE CASCADE,
    deployment_id UUID REFERENCES deployments(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    team_id UUID REFERENCES teams(id) ON DELETE SET NULL,
    
    -- Delivery configuration
    recipient_email VARCHAR(255),
    recipient_slack_channel VARCHAR(255),
    recipient_discord_channel VARCHAR(255),
    webhook_url TEXT,
    
    -- Delivery tracking
    sent_at TIMESTAMP WITH TIME ZONE,
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,
    
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- API tokens table for authentication and API access
CREATE TABLE api_tokens (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    token_hash VARCHAR(255) NOT NULL UNIQUE,
    permissions JSONB DEFAULT '{}',
    expires_at TIMESTAMP WITH TIME ZONE,
    last_used_at TIMESTAMP WITH TIME ZONE,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Audit logs table for tracking system activities
CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(100) NOT NULL,
    resource_type VARCHAR(100) NOT NULL,
    resource_id UUID,
    details JSONB DEFAULT '{}',
    ip_address INET,
    user_agent TEXT,
    timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Webhooks table for external integrations
CREATE TABLE webhooks (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    url TEXT NOT NULL,
    secret VARCHAR(255),
    events TEXT[] NOT NULL, -- Array of event types
    is_active BOOLEAN DEFAULT TRUE,
    last_triggered_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- System settings table for application configuration
CREATE TABLE system_settings (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    key VARCHAR(255) UNIQUE NOT NULL,
    value JSONB NOT NULL,
    description TEXT,
    is_secret BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for better query performance
CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_username ON users(username);
CREATE INDEX idx_users_status ON users(status);
CREATE INDEX idx_users_role ON users(role);

CREATE INDEX idx_teams_owner_id ON teams(owner_id);
CREATE INDEX idx_teams_slug ON teams(slug);

CREATE INDEX idx_team_memberships_team_id ON team_memberships(team_id);
CREATE INDEX idx_team_memberships_user_id ON team_memberships(user_id);

CREATE INDEX idx_projects_team_id ON projects(team_id);
CREATE INDEX idx_projects_created_by ON projects(created_by);
CREATE INDEX idx_projects_status ON projects(status);
CREATE INDEX idx_projects_slug ON projects(slug);

CREATE INDEX idx_deployments_project_id ON deployments(project_id);
CREATE INDEX idx_deployments_environment ON deployments(environment);
CREATE INDEX idx_deployments_status ON deployments(status);
CREATE INDEX idx_deployments_deployed_by ON deployments(deployed_by);
CREATE INDEX idx_deployments_created_at ON deployments(created_at);

CREATE INDEX idx_deployment_logs_deployment_id ON deployment_logs(deployment_id);
CREATE INDEX idx_deployment_logs_timestamp ON deployment_logs(timestamp);
CREATE INDEX idx_deployment_logs_level ON deployment_logs(log_level);

CREATE INDEX idx_notifications_project_id ON notifications(project_id);
CREATE INDEX idx_notifications_deployment_id ON notifications(deployment_id);
CREATE INDEX idx_notifications_user_id ON notifications(user_id);
CREATE INDEX idx_notifications_team_id ON notifications(team_id);
CREATE INDEX idx_notifications_status ON notifications(status);

CREATE INDEX idx_api_tokens_user_id ON api_tokens(user_id);
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);
CREATE INDEX idx_api_tokens_is_active ON api_tokens(is_active);

CREATE INDEX idx_audit_logs_user_id ON audit_logs(user_id);
CREATE INDEX idx_audit_logs_action ON audit_logs(action);
CREATE INDEX idx_audit_logs_resource_type ON audit_logs(resource_type);
CREATE INDEX idx_audit_logs_timestamp ON audit_logs(timestamp);

CREATE INDEX idx_webhooks_project_id ON webhooks(project_id);
CREATE INDEX idx_webhooks_is_active ON webhooks(is_active);

CREATE INDEX idx_system_settings_key ON system_settings(key);

-- Create updated_at trigger function
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- Apply updated_at triggers to relevant tables
CREATE TRIGGER update_users_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_teams_updated_at BEFORE UPDATE ON teams
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_projects_updated_at BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_deployments_updated_at BEFORE UPDATE ON deployments
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_notifications_updated_at BEFORE UPDATE ON notifications
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_webhooks_updated_at BEFORE UPDATE ON webhooks
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_system_settings_updated_at BEFORE UPDATE ON system_settings
    FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Insert default system settings
INSERT INTO system_settings (key, value, description) VALUES
('app_name', '"StackShip"', 'Application name'),
('app_version', '"1.0.0"', 'Application version'),
('max_deployment_retention_days', '90', 'Maximum days to retain deployment history'),
('default_cpu_request', '"100m"', 'Default CPU request for deployments'),
('default_memory_request', '"128Mi"', 'Default memory request for deployments'),
('default_cpu_limit', '"500m"', 'Default CPU limit for deployments'),
('default_memory_limit', '"512Mi"', 'Default memory limit for deployments'),
('webhook_timeout_seconds', '30', 'Webhook request timeout'),
('max_log_retention_days', '30', 'Maximum days to retain logs'),
('notification_retry_delay_seconds', '300', 'Delay between notification retries');

-- Create a default admin user (password should be changed on first login)
-- Password hash for 'admin123' - CHANGE THIS IN PRODUCTION
INSERT INTO users (email, password_hash, username, first_name, last_name, role, status, email_verified)
VALUES (
    'admin@stackship.dev',
    '$2a$12$LQv3c1yqBWVHxkd0LHAkCOYz6TtxMQJqhN8/LeNY1yHEYuKs6qfqW', -- admin123
    'admin',
    'System',
    'Administrator',
    'admin',
    'active',
    TRUE
);

-- Create a default team for the admin user
INSERT INTO teams (name, description, slug, owner_id)
VALUES (
    'Default Team',
    'Default team for system administration',
    'default',
    (SELECT id FROM users WHERE username = 'admin')
);

-- Add admin to the default team
INSERT INTO team_memberships (team_id, user_id, role)
VALUES (
    (SELECT id FROM teams WHERE slug = 'default'),
    (SELECT id FROM users WHERE username = 'admin'),
    'admin'
);

-- Add comments for documentation
COMMENT ON TABLE users IS 'User accounts for authentication and authorization';
COMMENT ON TABLE teams IS 'Teams for organizing users and projects';
COMMENT ON TABLE team_memberships IS 'Junction table for team membership relationships';
COMMENT ON TABLE projects IS 'Application projects managed by StackShip';
COMMENT ON TABLE deployments IS 'Deployment instances and their history';
COMMENT ON TABLE deployment_logs IS 'Logs generated during build and deployment processes';
COMMENT ON TABLE metrics IS 'Time-series metrics data for monitoring';
COMMENT ON TABLE notifications IS 'Notifications and alerts sent to users';
COMMENT ON TABLE api_tokens IS 'API authentication tokens';
COMMENT ON TABLE audit_logs IS 'Audit trail for system activities';
COMMENT ON TABLE webhooks IS 'External webhook integrations';
COMMENT ON TABLE system_settings IS 'System-wide configuration settings';