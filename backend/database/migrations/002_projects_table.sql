-- Migration: 002_projects_table.sql
-- Description: Create projects table with all necessary relationships and constraints
-- This table is central to the StackShip application and connects to users, deployments, and git repositories

-- Create projects table
CREATE TABLE IF NOT EXISTS projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    description TEXT,
    
    -- Repository information
    repository_url VARCHAR(500) NOT NULL,
    repository_type VARCHAR(50) NOT NULL DEFAULT 'git', -- git, github, gitlab, bitbucket
    repository_branch VARCHAR(100) NOT NULL DEFAULT 'main',
    repository_path VARCHAR(255) DEFAULT '/', -- Path within the repository
    
    -- Build configuration
    dockerfile_path VARCHAR(255) DEFAULT 'Dockerfile',
    build_context VARCHAR(255) DEFAULT '.',
    build_args JSONB DEFAULT '{}',
    
    -- Environment and deployment settings
    environment VARCHAR(50) NOT NULL DEFAULT 'development', -- development, staging, production
    deployment_type VARCHAR(50) NOT NULL DEFAULT 'kubernetes', -- kubernetes, docker, serverless
    
    -- Container configuration
    container_port INTEGER DEFAULT 8080,
    container_memory VARCHAR(20) DEFAULT '512Mi',
    container_cpu VARCHAR(20) DEFAULT '500m',
    
    -- Scaling configuration
    min_replicas INTEGER DEFAULT 1,
    max_replicas INTEGER DEFAULT 10,
    target_cpu_utilization INTEGER DEFAULT 80,
    
    -- Health check configuration
    health_check_path VARCHAR(255) DEFAULT '/health',
    readiness_probe_path VARCHAR(255) DEFAULT '/ready',
    liveness_probe_path VARCHAR(255) DEFAULT '/health',
    
    -- Environment variables and secrets
    environment_variables JSONB DEFAULT '{}',
    secrets JSONB DEFAULT '{}',
    
    -- Networking
    subdomain VARCHAR(100), -- For custom subdomain routing
    custom_domain VARCHAR(255), -- For custom domain mapping
    ssl_enabled BOOLEAN DEFAULT TRUE,
    
    -- Monitoring and logging
    monitoring_enabled BOOLEAN DEFAULT TRUE,
    logging_level VARCHAR(20) DEFAULT 'INFO',
    metrics_path VARCHAR(255) DEFAULT '/metrics',
    
    -- Auto-deployment settings
    auto_deploy_enabled BOOLEAN DEFAULT FALSE,
    auto_deploy_branch VARCHAR(100) DEFAULT 'main',
    webhook_secret VARCHAR(255), -- For git webhook verification
    
    -- Status and metadata
    status VARCHAR(50) NOT NULL DEFAULT 'active', -- active, inactive, archived, failed
    last_deployment_id UUID, -- Reference to last deployment
    last_build_status VARCHAR(50) DEFAULT 'pending', -- pending, building, success, failed
    last_build_at TIMESTAMP WITH TIME ZONE,
    
    -- Ownership and permissions
    owner_id UUID NOT NULL, -- Reference to users table
    team_id UUID, -- Reference to teams table (if team-based)
    visibility VARCHAR(20) DEFAULT 'private', -- private, internal, public
    
    -- Timestamps
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP,
    deleted_at TIMESTAMP WITH TIME ZONE, -- Soft delete
    
    -- Constraints
    CONSTRAINT projects_name_owner_unique UNIQUE (name, owner_id, deleted_at),
    CONSTRAINT projects_subdomain_unique UNIQUE (subdomain),
    CONSTRAINT projects_custom_domain_unique UNIQUE (custom_domain),
    CONSTRAINT projects_valid_status CHECK (status IN ('active', 'inactive', 'archived', 'failed')),
    CONSTRAINT projects_valid_environment CHECK (environment IN ('development', 'staging', 'production')),
    CONSTRAINT projects_valid_deployment_type CHECK (deployment_type IN ('kubernetes', 'docker', 'serverless')),
    CONSTRAINT projects_valid_visibility CHECK (visibility IN ('private', 'internal', 'public')),
    CONSTRAINT projects_valid_replicas CHECK (min_replicas > 0 AND max_replicas >= min_replicas),
    CONSTRAINT projects_valid_cpu_utilization CHECK (target_cpu_utilization > 0 AND target_cpu_utilization <= 100),
    CONSTRAINT projects_valid_port CHECK (container_port > 0 AND container_port <= 65535)
);

-- Create indexes for better performance
CREATE INDEX IF NOT EXISTS idx_projects_owner_id ON projects(owner_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_team_id ON projects(team_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_environment ON projects(environment) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_repository_url ON projects(repository_url) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_created_at ON projects(created_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_updated_at ON projects(updated_at) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_last_deployment_id ON projects(last_deployment_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_projects_auto_deploy ON projects(auto_deploy_enabled, auto_deploy_branch) WHERE deleted_at IS NULL;

-- Create partial unique index for soft-deleted records
CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_name_owner_active 
ON projects(name, owner_id) 
WHERE deleted_at IS NULL;

-- Create GIN indexes for JSONB columns for better query performance
CREATE INDEX IF NOT EXISTS idx_projects_environment_variables ON projects USING GIN(environment_variables);
CREATE INDEX IF NOT EXISTS idx_projects_secrets ON projects USING GIN(secrets);
CREATE INDEX IF NOT EXISTS idx_projects_build_args ON projects USING GIN(build_args);

-- Create trigger to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION update_projects_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_projects_updated_at
    BEFORE UPDATE ON projects
    FOR EACH ROW
    EXECUTE FUNCTION update_projects_updated_at();

-- Create function to validate project configuration
CREATE OR REPLACE FUNCTION validate_project_config()
RETURNS TRIGGER AS $$
BEGIN
    -- Validate memory format (e.g., 512Mi, 1Gi)
    IF NEW.container_memory !~ '^[0-9]+[MmGg][Ii]?$' THEN
        RAISE EXCEPTION 'Invalid memory format. Use format like 512Mi or 1Gi';
    END IF;
    
    -- Validate CPU format (e.g., 500m, 1, 2)
    IF NEW.container_cpu !~ '^[0-9]+[m]?$' THEN
        RAISE EXCEPTION 'Invalid CPU format. Use format like 500m or 1';
    END IF;
    
    -- Validate repository URL format
    IF NEW.repository_url !~ '^https?://' THEN
        RAISE EXCEPTION 'Repository URL must start with http:// or https://';
    END IF;
    
    -- Validate paths don't contain dangerous characters
    IF NEW.dockerfile_path ~ '[;&|`$]' OR NEW.build_context ~ '[;&|`$]' OR NEW.repository_path ~ '[;&|`$]' THEN
        RAISE EXCEPTION 'Paths cannot contain dangerous characters';
    END IF;
    
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_validate_project_config
    BEFORE INSERT OR UPDATE ON projects
    FOR EACH ROW
    EXECUTE FUNCTION validate_project_config();

-- Create function for soft delete
CREATE OR REPLACE FUNCTION soft_delete_project(project_id UUID)
RETURNS BOOLEAN AS $$
BEGIN
    UPDATE projects 
    SET deleted_at = CURRENT_TIMESTAMP,
        status = 'archived'
    WHERE id = project_id AND deleted_at IS NULL;
    
    RETURN FOUND;
END;
$$ LANGUAGE plpgsql;

-- Create function to generate subdomain from project name
CREATE OR REPLACE FUNCTION generate_subdomain(project_name TEXT, owner_id UUID)
RETURNS TEXT AS $$
DECLARE
    base_subdomain TEXT;
    final_subdomain TEXT;
    counter INTEGER := 0;
BEGIN
    -- Clean project name to create valid subdomain
    base_subdomain := lower(regexp_replace(project_name, '[^a-zA-Z0-9-]', '-', 'g'));
    base_subdomain := regexp_replace(base_subdomain, '-+', '-', 'g');
    base_subdomain := trim(base_subdomain, '-');
    
    -- Ensure subdomain is not empty and not too long
    IF length(base_subdomain) = 0 THEN
        base_subdomain := 'project';
    END IF;
    
    IF length(base_subdomain) > 50 THEN
        base_subdomain := left(base_subdomain, 50);
    END IF;
    
    final_subdomain := base_subdomain;
    
    -- Check for uniqueness and add counter if needed
    WHILE EXISTS (SELECT 1 FROM projects WHERE subdomain = final_subdomain AND deleted_at IS NULL) LOOP
        counter := counter + 1;
        final_subdomain := base_subdomain || '-' || counter;
    END LOOP;
    
    RETURN final_subdomain;
END;
$$ LANGUAGE plpgsql;

-- Create trigger to auto-generate subdomain if not provided
CREATE OR REPLACE FUNCTION auto_generate_subdomain()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.subdomain IS NULL OR NEW.subdomain = '' THEN
        NEW.subdomain := generate_subdomain(NEW.name, NEW.owner_id);
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_auto_generate_subdomain
    BEFORE INSERT ON projects
    FOR EACH ROW
    EXECUTE FUNCTION auto_generate_subdomain();

-- Create view for active projects with computed fields
CREATE OR REPLACE VIEW active_projects AS
SELECT 
    p.*,
    CASE 
        WHEN p.custom_domain IS NOT NULL THEN p.custom_domain
        WHEN p.subdomain IS NOT NULL THEN p.subdomain || '.stackship.dev'
        ELSE p.id::TEXT || '.stackship.dev'
    END as full_domain,
    CASE 
        WHEN p.last_build_at IS NULL THEN 'never'
        WHEN p.last_build_at < CURRENT_TIMESTAMP - INTERVAL '1 day' THEN 'stale'
        ELSE 'recent'
    END as build_freshness,
    CASE 
        WHEN p.auto_deploy_enabled THEN 'auto'
        ELSE 'manual'
    END as deployment_mode
FROM projects p
WHERE p.deleted_at IS NULL;

-- Create view for project statistics
CREATE OR REPLACE VIEW project_stats AS
SELECT 
    owner_id,
    COUNT(*) as total_projects,
    COUNT(CASE WHEN status = 'active' THEN 1 END) as active_projects,
    COUNT(CASE WHEN status = 'inactive' THEN 1 END) as inactive_projects,
    COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed_projects,
    COUNT(CASE WHEN auto_deploy_enabled = true THEN 1 END) as auto_deploy_projects,
    COUNT(CASE WHEN environment = 'production' THEN 1 END) as production_projects,
    MAX(created_at) as last_project_created,
    MAX(updated_at) as last_project_updated
FROM projects
WHERE deleted_at IS NULL
GROUP BY owner_id;

-- Add comments for documentation
COMMENT ON TABLE projects IS 'Central table storing all project configurations and metadata for StackShip deployments';
COMMENT ON COLUMN projects.id IS 'Unique identifier for the project';
COMMENT ON COLUMN projects.name IS 'Human-readable project name, unique per owner';
COMMENT ON COLUMN projects.repository_url IS 'Git repository URL for source code';
COMMENT ON COLUMN projects.deployment_type IS 'Target deployment platform (kubernetes, docker, serverless)';
COMMENT ON COLUMN projects.environment_variables IS 'JSON object storing environment variables for the application';
COMMENT ON COLUMN projects.secrets IS 'JSON object storing secret references (not actual secret values)';
COMMENT ON COLUMN projects.webhook_secret IS 'Secret token for validating git webhooks';
COMMENT ON COLUMN projects.last_deployment_id IS 'Reference to the most recent deployment';
COMMENT ON COLUMN projects.owner_id IS 'Reference to the user who owns this project';
COMMENT ON COLUMN projects.deleted_at IS 'Timestamp for soft deletion, NULL for active records';

-- Grant appropriate permissions (adjust based on your RBAC setup)
-- These would typically be run by a database admin
-- GRANT SELECT, INSERT, UPDATE ON projects TO stackship_api;
-- GRANT SELECT ON active_projects TO stackship_api;
-- GRANT SELECT ON project_stats TO stackship_api;
-- GRANT EXECUTE ON FUNCTION soft_delete_project TO stackship_api;