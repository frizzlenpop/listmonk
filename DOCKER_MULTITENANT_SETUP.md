# Multi-Tenant Listmonk Docker Setup Guide

This guide provides comprehensive instructions for deploying Listmonk with multi-tenancy support using Docker. The implementation includes tenant isolation, subdomain/domain routing, and production-ready configurations.

## 🚀 Quick Start

### Prerequisites
- Docker 20.10+ and Docker Compose 2.0+
- PostgreSQL 12+ (included in docker-compose)
- Domain name with DNS management access
- SSL certificates (for production)

### Basic Multi-Tenant Setup

1. **Clone and prepare the repository:**
```bash
git clone https://github.com/knadh/listmonk.git
cd listmonk
```

2. **Create environment file:**
```bash
cp .env.sample .env
# Edit .env with your configuration (see Environment Variables section)
```

3. **Start the multi-tenant stack:**
```bash
# Development/Testing
docker-compose -f docker-compose.multitenant.yml up -d

# Production
docker-compose -f docker-compose.production.yml up -d
```

4. **Initialize first tenant:**
```bash
# The default tenant is automatically created during first startup
# Access via: http://localhost:9000 (development)
```

## 📁 File Structure

The multi-tenant Docker setup includes these new files:

```
listmonk/
├── docker-compose.multitenant.yml      # Multi-tenant development setup
├── docker-compose.production.yml       # Production-ready configuration
├── Dockerfile.multitenant             # Enhanced Dockerfile with tenant support
├── docker-entrypoint-multitenant.sh   # Enhanced entrypoint script
├── scripts/
│   ├── init-tenants.sh                # Tenant initialization script
│   ├── health-check.sh                # Multi-tenant health checks
│   └── backup-tenant.sh               # Tenant-specific backup script
└── DOCKER_MULTITENANT_SETUP.md        # This guide
```

## 🔧 Configuration Files

### docker-compose.multitenant.yml
- **Purpose**: Development and testing multi-tenant setup
- **Features**: 
  - PostgreSQL with performance tuning
  - Redis for caching (optional)
  - Comprehensive environment variables
  - Tenant-segregated volumes
  - Health checks

### docker-compose.production.yml
- **Purpose**: Production deployment with full security
- **Features**:
  - Docker secrets management
  - SSL/TLS termination
  - Resource limits and monitoring
  - Nginx reverse proxy
  - Prometheus + Grafana monitoring
  - Network isolation
  - Enhanced security settings

### Dockerfile.multitenant
- **Purpose**: Enhanced Docker image with multi-tenant features
- **Features**:
  - Non-root user execution
  - Tenant directory structure
  - Security hardening
  - Health check integration
  - Multi-stage build support

## 🌐 Environment Variables

### Core Multi-Tenancy Settings

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTMONK_MULTITENANCY_ENABLED` | `true` | Enable/disable multi-tenancy |
| `LISTMONK_DEFAULT_TENANT_ID` | `1` | Default tenant ID for fallback |
| `LISTMONK_SUPER_ADMIN_EMAIL` | `admin@localhost` | Super admin user email |
| `LISTMONK_DEFAULT_TENANT_NAME` | `Default Organization` | Name of the default tenant |
| `LISTMONK_DEFAULT_TENANT_SLUG` | `default` | URL slug for default tenant |

### Domain and Routing Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTMONK_TENANT_BASE_DOMAIN` | `localhost` | Base domain for subdomains |
| `LISTMONK_TENANT_DOMAIN_SUFFIX` | `.listmonk.localhost` | Suffix for tenant subdomains |
| `BASE_DOMAIN` | `yourdomain.com` | Your primary domain (production) |

### Tenant Resolution Methods

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTMONK_TENANT_RESOLUTION_SUBDOMAIN` | `true` | Enable subdomain-based routing |
| `LISTMONK_TENANT_RESOLUTION_DOMAIN` | `true` | Enable custom domain routing |
| `LISTMONK_TENANT_RESOLUTION_HEADER` | `true` | Enable X-Tenant-ID header routing |
| `LISTMONK_TENANT_RESOLUTION_QUERY` | `false` | Enable ?tenant= query parameter (dev only) |

### Security and Isolation

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTMONK_TENANT_STRICT_MODE` | `true` | Enforce strict tenant isolation |
| `LISTMONK_TENANT_AUTO_CREATE` | `false` | Auto-create tenants on first access |
| `LISTMONK_TENANT_UPLOADS_SEGREGATED` | `true` | Separate upload directories per tenant |

### Performance and Caching

| Variable | Default | Description |
|----------|---------|-------------|
| `LISTMONK_TENANT_CACHE_ENABLED` | `true` | Enable tenant caching |
| `LISTMONK_TENANT_CACHE_TTL` | `600` | Cache TTL in seconds |
| `LISTMONK_TENANT_REDIS_HOST` | - | Redis host for caching |
| `LISTMONK_TENANT_REDIS_PASSWORD` | - | Redis password |

### File Storage

| Variable | Default | Description |
|----------|---------|-------------|
| `S3_ENABLED` | `false` | Enable S3 storage for uploads |
| `S3_BUCKET` | - | S3 bucket name |
| `S3_REGION` | - | S3 region |
| `S3_ACCESS_KEY` | - | S3 access key |
| `S3_SECRET_KEY` | - | S3 secret key |

### Monitoring and Logging

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` | Logging level (debug, info, warn, error) |
| `PROMETHEUS_ENABLED` | `true` | Enable Prometheus metrics |
| `GRAFANA_ADMIN_USER` | `admin` | Grafana admin username |
| `GRAFANA_ADMIN_PASSWORD` | - | Grafana admin password |

## 🏗️ Deployment Scenarios

### Development Setup

Create `.env` file:
```bash
# Basic multi-tenancy
LISTMONK_MULTITENANCY_ENABLED=true
LISTMONK_DEFAULT_TENANT_NAME="My Development Org"
LISTMONK_SUPER_ADMIN_EMAIL=admin@localhost

# Database
LISTMONK_ADMIN_USER=admin
LISTMONK_ADMIN_PASSWORD=password123

# Domain configuration (for development)
LISTMONK_TENANT_BASE_DOMAIN=localhost
LISTMONK_TENANT_DOMAIN_SUFFIX=.listmonk.localhost
```

Start development stack:
```bash
docker-compose -f docker-compose.multitenant.yml up -d
```

### Production Setup

Create `.env` file:
```bash
# Production multi-tenancy
LISTMONK_MULTITENANCY_ENABLED=true
BASE_DOMAIN=yourdomain.com
SUPER_ADMIN_EMAIL=admin@yourdomain.com

# Security
LISTMONK_TENANT_STRICT_MODE=true
LISTMONK_TENANT_AUTO_CREATE=false

# Performance
LISTMONK_TENANT_CACHE_ENABLED=true
REDIS_PASSWORD=your-redis-password

# Monitoring
GRAFANA_ADMIN_PASSWORD=secure-grafana-password

# SSL/Storage
S3_ENABLED=true
S3_BUCKET=your-listmonk-bucket
S3_REGION=us-east-1
S3_ACCESS_KEY=your-access-key
S3_SECRET_KEY=your-secret-key
```

Create Docker secrets:
```bash
# Create secrets directory
mkdir -p secrets

# Create secret files
echo "your-db-password" | docker secret create postgres_password -
echo "your-admin-password" | docker secret create listmonk_admin_password -
echo "your-jwt-secret" | docker secret create jwt_secret -
```

Start production stack:
```bash
docker-compose -f docker-compose.production.yml up -d
```

## 🌍 DNS Configuration

### Subdomain Setup (Wildcard DNS)

For tenant subdomains like `acme.listmonk.yourdomain.com`:

```dns
*.listmonk.yourdomain.com    CNAME    your-listmonk-server.com
```

### Custom Domain Setup

For tenant custom domains like `mail.acme.com`:

```dns
mail.acme.com               CNAME    your-listmonk-server.com
```

### Load Balancer Configuration (if using)

```nginx
# Nginx upstream configuration
upstream listmonk_backend {
    server listmonk_app:9000;
}

server {
    listen 80;
    server_name *.listmonk.yourdomain.com yourdomain.com;
    
    location / {
        proxy_pass http://listmonk_backend;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

## 🛠️ Tenant Management

### Creating Tenants via API

```bash
# Create a new tenant
curl -X POST http://localhost:9000/api/tenants \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
  -d '{
    "name": "Acme Corporation",
    "slug": "acme",
    "domain": "mail.acme.com",
    "plan": "premium",
    "features": {
      "max_subscribers": 50000,
      "max_campaigns_per_month": 500,
      "custom_domain": true,
      "api_access": true
    }
  }'
```

### Creating Tenants via Database

```sql
-- Connect to database
docker exec -it listmonk_db_prod psql -U listmonk -d listmonk

-- Create tenant
INSERT INTO tenants (uuid, name, slug, domain, status, plan, features) 
VALUES (
  gen_random_uuid(),
  'Acme Corporation',
  'acme',
  'mail.acme.com',
  'active',
  'premium',
  '{"max_subscribers": 50000, "custom_domain": true}'::jsonb
);
```

### Accessing Tenants

1. **Subdomain**: `https://acme.listmonk.yourdomain.com`
2. **Custom Domain**: `https://mail.acme.com`
3. **Header**: `curl -H "X-Tenant-ID: 2" http://localhost:9000/api/config`
4. **Query Parameter** (dev only): `http://localhost:9000?tenant=acme`

## 🔒 Security Considerations

### Production Security Checklist

- [ ] Use Docker secrets for passwords and keys
- [ ] Enable SSL/TLS with valid certificates
- [ ] Configure firewall rules (only 80/443 exposed)
- [ ] Use non-root user in containers
- [ ] Enable Row Level Security (RLS) in database
- [ ] Implement rate limiting on reverse proxy
- [ ] Set up log monitoring and alerting
- [ ] Regular security updates for base images
- [ ] Backup encryption at rest

### Network Security

```yaml
# Example production network configuration
networks:
  listmonk_internal:
    driver: bridge
    internal: true  # No external access
  web_public:
    driver: bridge  # External access only for web traffic
```

## 📊 Monitoring and Maintenance

### Health Checks

```bash
# Comprehensive health check
docker exec listmonk_app /usr/local/bin/health-check.sh full

# Quick health check
docker exec listmonk_app /usr/local/bin/health-check.sh minimal

# Check specific tenant
curl -H "X-Tenant-ID: 1" http://localhost:9000/api/health
```

### Backup Operations

```bash
# Backup all tenants
docker exec listmonk_app /usr/local/bin/backup-tenant.sh all

# Backup specific tenant
docker exec listmonk_app /usr/local/bin/backup-tenant.sh 2

# Cleanup old backups only
docker exec listmonk_app /usr/local/bin/backup-tenant.sh --cleanup-only
```

### Performance Monitoring

Access Grafana dashboard at `http://your-domain:3000` (production setup) to monitor:

- Tenant-specific metrics
- Database performance per tenant
- Resource usage by tenant
- API response times
- Error rates

### Log Analysis

```bash
# View application logs
docker logs listmonk_app_prod -f

# View database logs
docker logs listmonk_db_prod -f

# View tenant-specific logs
docker exec listmonk_app grep "tenant_id=2" /var/log/listmonk/*.log
```

## 🚨 Troubleshooting

### Common Issues

#### Tenant Not Found
```bash
# Check DNS resolution
nslookup acme.listmonk.yourdomain.com

# Check tenant exists in database
docker exec listmonk_db_prod psql -U listmonk -d listmonk -c "SELECT * FROM tenants WHERE slug = 'acme';"

# Check middleware configuration
docker logs listmonk_app_prod | grep -i tenant
```

#### Performance Issues
```bash
# Check database performance
docker exec listmonk_db_prod psql -U listmonk -d listmonk -c "SELECT * FROM pg_stat_statements ORDER BY mean_time DESC LIMIT 10;"

# Check resource usage
docker stats listmonk_app_prod listmonk_db_prod

# Check tenant-specific query performance
docker exec listmonk_db_prod psql -U listmonk -d listmonk -c "EXPLAIN ANALYZE SELECT * FROM subscribers WHERE tenant_id = 1 LIMIT 10;"
```

#### Cross-Tenant Data Leakage
```bash
# Verify RLS policies
docker exec listmonk_db_prod psql -U listmonk -d listmonk -c "SELECT tablename, policyname FROM pg_policies WHERE policyname LIKE '%tenant%';"

# Test tenant isolation
docker exec listmonk_app /listmonk/test_tenant_isolation.sql
```

### Recovery Procedures

#### Restore Tenant from Backup
```bash
# Stop application
docker stop listmonk_app_prod

# Restore database
gunzip -c /backup/tenant_2_backup_20240101_120000.sql.gz | \
  docker exec -i listmonk_db_prod psql -U listmonk -d listmonk

# Restore files
docker exec listmonk_app tar -xzf /backup/tenant_2_files_20240101_120000.tar.gz -C /listmonk/uploads/tenants/

# Start application
docker start listmonk_app_prod
```

#### Migrate Tenant to New Instance
```bash
# Export tenant data
docker exec listmonk_app /usr/local/bin/backup-tenant.sh 2

# Transfer to new instance
scp /backup/tenant_2_* new-server:/backup/

# Import on new instance
docker exec new_listmonk_app psql -U listmonk -d listmonk < /backup/tenant_2_backup_*.sql
```

## 📚 Additional Resources

### API Documentation
- Tenant management: `/api/tenants`
- User-tenant relationships: `/api/user/tenants`
- Tenant switching: `/api/user/tenants/switch/:id`

### Database Schema
- Multi-tenancy tables: `tenants`, `user_tenants`, `tenant_settings`
- RLS policies on all tenant data tables
- Composite indexes for performance

### Integration Examples
- Subdomain routing with Traefik
- Custom domain management
- Webhook configuration per tenant
- API rate limiting per tenant

---

## 🤝 Support

For issues specific to multi-tenant Docker deployment:

1. Check container logs: `docker logs listmonk_app_prod`
2. Run health checks: `docker exec listmonk_app /usr/local/bin/health-check.sh`
3. Verify tenant isolation: Review RLS policies and test cross-tenant access
4. Performance issues: Use included monitoring tools (Prometheus/Grafana)

For general Listmonk issues, refer to the main [Listmonk documentation](https://listmonk.app/docs/).