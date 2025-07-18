-- Migration: 003_deployments_table.sql
-- Description: Create deployments table and related tables for StackShip application
-- This migration establishes the core deployment management functionality

-- Create deployments table
CREATE TABLE IF NOT EXISTS deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE SET NULL,
    
    -- Deployment identification
    name VARCHAR(255) NOT NULL,
    description TEXT,
    version VARCHAR(100) NOT NULL,
    git_commit_hash VARCHAR(40),
    git_branch VARCHAR(255) DEFAULT 'main',
    
    -- Deployment configuration
    environment VARCHAR(50) NOT NULL CHECK (environment IN ('development', 'staging', 'production')),
    deployment_type VARCHAR(50) NOT NULL CHECK (deployment_type IN ('docker', 'kubernetes', 'manual')),
    docker_image VARCHAR(500),
    kubernetes_namespace VARCHAR(63),
    
    -- Resource configuration
    cpu_limit VARCHAR(50) DEFAULT '500m',
    memory_limit VARCHAR(50) DEFAULT '512Mi',
    cpu_request VARCHAR(50) DEFAULT '100m',
    memory_request VARCHAR(50) DEFAULT '128Mi',
    replicas INTEGER DEFAULT 1,
    
    -- Deployment status and lifecycle
    status VARCHAR(50) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'building', 'deploying', 'running', 'failed', 'stopped', 'rollback')),
    health_status VARCHAR(50) DEFAULT 'unknown' CHECK (health_status IN ('healthy', 'unhealthy', 'unknown')),
    
    -- Deployment URLs and endpoints
    deployment_url VARCHAR(500),
    internal_url VARCHAR(500),
    
    -- Configuration and secrets
    env_variables JSONB DEFAULT '{}',
    secrets JSONB DEFAULT '{}',
    config_maps JSONB DEFAULT '{}',
    
    -- Deployment strategy
    strategy VARCHAR(50) DEFAULT 'rolling' CHECK (strategy IN ('rolling', 'blue-green', 'canary', 'recreate')),
    auto_scaling BOOLEAN DEFAULT FALSE,
    min_replicas INTEGER DEFAULT 1,
    max_replicas INTEGER DEFAULT 10,
    
    -- Monitoring and alerting
    monitoring_enabled BOOLEAN DEFAULT TRUE,
    alerting_enabled BOOLEAN DEFAULT TRUE,
    log_retention_days INTEGER DEFAULT 30,
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deployed_at TIMESTAMP WITH TIME ZONE,
    last_health_check TIMESTAMP WITH TIME ZONE,
    
    -- Constraints
    UNIQUE(project_id, name, environment),
    CHECK (replicas >= 1),
    CHECK (min_replicas <= max_replicas)
);

-- Create deployment_logs table for tracking deployment activities
CREATE TABLE IF NOT EXISTS deployment_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    
    -- Log details
    log_level VARCHAR(20) NOT NULL CHECK (log_level IN ('DEBUG', 'INFO', 'WARN', 'ERROR', 'FATAL')),
    message TEXT NOT NULL,
    source VARCHAR(100) NOT NULL, -- docker, kubernetes, pipeline, etc.
    step_name VARCHAR(100),
    
    -- Metadata
    metadata JSONB DEFAULT '{}',
    container_name VARCHAR(255),
    pod_name VARCHAR(255),
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    log_timestamp TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create deployment_metrics table for storing performance metrics
CREATE TABLE IF NOT EXISTS deployment_metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    
    -- Metric details
    metric_name VARCHAR(100) NOT NULL,
    metric_value DECIMAL(15,4) NOT NULL,
    metric_unit VARCHAR(20),
    metric_type VARCHAR(50) NOT NULL CHECK (metric_type IN ('counter', 'gauge', 'histogram', 'summary')),
    
    -- Labels for metric identification
    labels JSONB DEFAULT '{}',
    
    -- Timestamps
    collected_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create deployment_events table for audit trail and notifications
CREATE TABLE IF NOT EXISTS deployment_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    
    -- Event details
    event_type VARCHAR(50) NOT NULL CHECK (event_type IN ('created', 'updated', 'deployed', 'stopped', 'scaled', 'failed', 'rollback', 'health_check')),
    event_status VARCHAR(20) NOT NULL CHECK (event_status IN ('success', 'failure', 'in_progress')),
    message TEXT,
    
    -- Additional context
    old_values JSONB DEFAULT '{}',
    new_values JSONB DEFAULT '{}',
    error_details TEXT,
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create deployment_dependencies table for managing service dependencies
CREATE TABLE IF NOT EXISTS deployment_dependencies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    depends_on_deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    
    -- Dependency configuration
    dependency_type VARCHAR(50) NOT NULL CHECK (dependency_type IN ('service', 'database', 'cache', 'queue', 'storage')),
    is_required BOOLEAN DEFAULT TRUE,
    health_check_endpoint VARCHAR(500),
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    
    -- Constraints
    CHECK (deployment_id != depends_on_deployment_id),
    UNIQUE(deployment_id, depends_on_deployment_id)
);

-- Create deployment_rollbacks table for version control
CREATE TABLE IF NOT EXISTS deployment_rollbacks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE SET NULL,
    
    -- Rollback details
    from_version VARCHAR(100) NOT NULL,
    to_version VARCHAR(100) NOT NULL,
    reason TEXT NOT NULL,
    rollback_status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (rollback_status IN ('pending', 'in_progress', 'completed', 'failed')),
    
    -- Configuration backup
    previous_config JSONB NOT NULL,
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP WITH TIME ZONE
);

-- Create deployment_webhooks table for CI/CD integration
CREATE TABLE IF NOT EXISTS deployment_webhooks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    deployment_id UUID NOT NULL REFERENCES deployments(id) ON DELETE CASCADE,
    
    -- Webhook configuration
    webhook_url VARCHAR(500) NOT NULL,
    webhook_secret VARCHAR(255),
    webhook_type VARCHAR(50) NOT NULL CHECK (webhook_type IN ('github', 'gitlab', 'bitbucket', 'generic')),
    
    -- Trigger configuration
    trigger_events TEXT[] DEFAULT ARRAY['push', 'pull_request'],
    auto_deploy BOOLEAN DEFAULT FALSE,
    branch_filter VARCHAR(255) DEFAULT 'main',
    
    -- Status
    is_active BOOLEAN DEFAULT TRUE,
    last_triggered TIMESTAMP WITH TIME ZONE,
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for better performance
CREATE INDEX IF NOT EXISTS idx_deployments_project_id ON deployments(project_id);
CREATE INDEX IF NOT EXISTS idx_deployments_user_id ON deployments(user_id);
CREATE INDEX IF NOT EXISTS idx_deployments_status ON deployments(status);
CREATE INDEX IF NOT EXISTS idx_deployments_environment ON deployments(environment);
CREATE INDEX IF NOT EXISTS idx_deployments_created_at ON deployments(created_at);
CREATE INDEX IF NOT EXISTS idx_deployments_project_env ON deployments(project_id, environment);

CREATE INDEX IF NOT EXISTS idx_deployment_logs_deployment_id ON deployment_logs(deployment_id);
CREATE INDEX IF NOT EXISTS idx_deployment_logs_level ON deployment_logs(log_level);
CREATE INDEX IF NOT EXISTS idx_deployment_logs_created_at ON deployment_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_deployment_logs_source ON deployment_logs(source);

CREATE INDEX IF NOT EXISTS idx_deployment_metrics_deployment_id ON deployment_metrics(deployment_id);
CREATE INDEX IF NOT EXISTS idx_deployment_metrics_name ON deployment_metrics(metric_name);
CREATE INDEX IF NOT EXISTS idx_deployment_metrics_collected_at ON deployment_metrics(collected_at);

CREATE INDEX IF NOT EXISTS idx_deployment_events_deployment_id ON deployment_events(deployment_id);
CREATE INDEX IF NOT EXISTS idx_deployment_events_type ON deployment_events(event_type);
CREATE INDEX IF NOT EXISTS idx_deployment_events_created_at ON deployment_events(created_at);

CREATE INDEX IF NOT EXISTS idx_deployment_dependencies_deployment_id ON deployment_dependencies(deployment_id);
CREATE INDEX IF NOT EXISTS idx_deployment_dependencies_depends_on ON deployment_dependencies(depends_on_deployment_id);

CREATE INDEX IF NOT EXISTS idx_deployment_rollbacks_deployment_id ON deployment_rollbacks(deployment_id);
CREATE INDEX IF NOT EXISTS idx_deployment_rollbacks_created_at ON deployment_rollbacks(created_at);

CREATE INDEX IF NOT EXISTS idx_deployment_webhooks_deployment_id ON deployment_webhooks(deployment_id);
CREATE INDEX IF NOT EXISTS idx_deployment_webhooks_active ON deployment_webhooks(is_active);

-- Create function to update updated_at timestamp
CREATE OR REPLACE FUNCTION update_deployment_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for deployments table
CREATE TRIGGER trigger_deployments_updated_at
    BEFORE UPDATE ON deployments
    FOR EACH ROW
    EXECUTE FUNCTION update_deployment_updated_at();

-- Create function to auto-create deployment event on status change
CREATE OR REPLACE FUNCTION create_deployment_event()
RETURNS TRIGGER AS $$
BEGIN
    -- Only create event if status changed
    IF OLD.status IS DISTINCT FROM NEW.status THEN
        INSERT INTO deployment_events (deployment_id, event_type, event_status, message, old_values, new_values)
        VALUES (
            NEW.id,
            CASE 
                WHEN NEW.status = 'running' THEN 'deployed'
                WHEN NEW.status = 'failed' THEN 'failed'
                WHEN NEW.status = 'stopped' THEN 'stopped'
                ELSE 'updated'
            END,
            CASE 
                WHEN NEW.status IN ('running', 'stopped') THEN 'success'
                WHEN NEW.status = 'failed' THEN 'failure'
                ELSE 'in_progress'
            END,
            'Deployment status changed from ' || COALESCE(OLD.status, 'unknown') || ' to ' || NEW.status,
            jsonb_build_object('status', OLD.status),
            jsonb_build_object('status', NEW.status)
        );
    END IF;
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for deployment status changes
CREATE TRIGGER trigger_deployment_status_event
    AFTER UPDATE ON deployments
    FOR EACH ROW
    EXECUTE FUNCTION create_deployment_event();

-- Create function for deployment health checks
CREATE OR REPLACE FUNCTION update_deployment_health()
RETURNS TRIGGER AS $$
BEGIN
    -- Update last health check timestamp
    NEW.last_health_check = CURRENT_TIMESTAMP;
    
    -- Log health check result
    INSERT INTO deployment_logs (deployment_id, log_level, message, source, step_name)
    VALUES (
        NEW.id,
        CASE 
            WHEN NEW.health_status = 'healthy' THEN 'INFO'
            WHEN NEW.health_status = 'unhealthy' THEN 'ERROR'
            ELSE 'WARN'
        END,
        'Health check result: ' || NEW.health_status,
        'health_check',
        'health_monitor'
    );
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create trigger for health status changes
CREATE TRIGGER trigger_deployment_health_check
    BEFORE UPDATE OF health_status ON deployments
    FOR EACH ROW
    WHEN (OLD.health_status IS DISTINCT FROM NEW.health_status)
    EXECUTE FUNCTION update_deployment_health();

-- Create views for common queries
CREATE OR REPLACE VIEW deployment_summary AS
SELECT 
    d.id,
    d.name,
    d.version,
    d.environment,
    d.status,
    d.health_status,
    d.deployment_url,
    d.replicas,
    d.created_at,
    d.deployed_at,
    p.name as project_name,
    u.username as deployed_by,
    COUNT(dl.id) as log_count,
    MAX(dl.created_at) as last_log_time
FROM deployments d
LEFT JOIN projects p ON d.project_id = p.id
LEFT JOIN users u ON d.user_id = u.id
LEFT JOIN deployment_logs dl ON d.id = dl.deployment_id
GROUP BY d.id, p.name, u.username;

-- Create view for deployment metrics summary
CREATE OR REPLACE VIEW deployment_metrics_summary AS
SELECT 
    dm.deployment_id,
    d.name as deployment_name,
    d.environment,
    COUNT(dm.id) as total_metrics,
    COUNT(DISTINCT dm.metric_name) as unique_metrics,
    MAX(dm.collected_at) as last_metric_time,
    AVG(CASE WHEN dm.metric_name = 'cpu_usage' THEN dm.metric_value END) as avg_cpu_usage,
    AVG(CASE WHEN dm.metric_name = 'memory_usage' THEN dm.metric_value END) as avg_memory_usage,
    AVG(CASE WHEN dm.metric_name = 'response_time' THEN dm.metric_value END) as avg_response_time
FROM deployment_metrics dm
JOIN deployments d ON dm.deployment_id = d.id
GROUP BY dm.deployment_id, d.name, d.environment;

-- Insert default data or configuration
INSERT INTO deployment_events (deployment_id, event_type, event_status, message)
SELECT id, 'created', 'success', 'Deployment created during migration'
FROM deployments
WHERE NOT EXISTS (
    SELECT 1 FROM deployment_events 
    WHERE deployment_id = deployments.id 
    AND event_type = 'created'
);

-- Grant permissions (adjust based on your role system)
GRANT SELECT, INSERT, UPDATE, DELETE ON deployments TO stackship_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON deployment_logs TO stackship_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON deployment_metrics TO stackship_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON deployment_events TO stackship_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON deployment_dependencies TO stackship_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON deployment_rollbacks TO stackship_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON deployment_webhooks TO stackship_app;

GRANT SELECT ON deployment_summary TO stackship_app;
GRANT SELECT ON deployment_metrics_summary TO stackship_app;

-- Grant sequence permissions
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO stackship_app;

-- Add comments for documentation
COMMENT ON TABLE deployments IS 'Core deployment configuration and status tracking';
COMMENT ON TABLE deployment_logs IS 'Deployment activity logs for debugging and monitoring';
COMMENT ON TABLE deployment_metrics IS 'Performance metrics collected from deployments';
COMMENT ON TABLE deployment_events IS 'Audit trail of deployment lifecycle events';
COMMENT ON TABLE deployment_dependencies IS 'Service dependencies between deployments';
COMMENT ON TABLE deployment_rollbacks IS 'Rollback history and configuration';
COMMENT ON TABLE deployment_webhooks IS 'CI/CD webhook configurations for automated deployments';

COMMENT ON COLUMN deployments.status IS 'Current deployment status: pending, building, deploying, running, failed, stopped, rollback';
COMMENT ON COLUMN deployments.health_status IS 'Health check status: healthy, unhealthy, unknown';
COMMENT ON COLUMN deployments.deployment_type IS 'Deployment platform: docker, kubernetes, manual';
COMMENT ON COLUMN deployments.strategy IS 'Deployment strategy: rolling, blue-green, canary, recreate';
COMMENT ON COLUMN deployments.env_variables IS 'Environment variables as JSON object';
COMMENT ON COLUMN deployments.secrets IS 'Encrypted secrets configuration';
COMMENT ON COLUMN deployments.config_maps IS 'Configuration maps for Kubernetes deployments';