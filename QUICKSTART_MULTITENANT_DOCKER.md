# 🚀 Listmonk Multi-Tenant Docker Quick Start Guide

This guide will get you up and running with multi-tenant Listmonk in Docker within 10 minutes.

## ⚡ Quick Deploy (TL;DR)

```bash
# 1. Clone and navigate
git clone <your-repo>
cd listmonk

# 2. Copy environment file
cp .env.multitenant.sample .env

# 3. Edit configuration (required!)
nano .env  # Or your preferred editor

# 4. Deploy
chmod +x scripts/deploy-multitenant.sh
./scripts/deploy-multitenant.sh

# 5. Access
open http://localhost:9000/admin
```

## 📋 Prerequisites

- Docker 20.10+
- Docker Compose 2.0+
- 2GB RAM minimum
- 10GB disk space minimum

## 🔧 Step-by-Step Installation

### Step 1: Environment Configuration

Copy the sample environment file:
```bash
cp .env.multitenant.sample .env
```

**REQUIRED CHANGES** in `.env`:
```env
# Database Configuration
POSTGRES_PASSWORD=your-secure-password-here

# Multi-Tenancy Settings
LISTMONK_TENANT_MODE=true
LISTMONK_TENANT_STRATEGY=subdomain
LISTMONK_TENANT_DOMAIN_SUFFIX=.yourdomain.com

# Application Settings
LISTMONK_app__from_email="Your App <noreply@yourdomain.com>"
LISTMONK_app__root_url=http://localhost:9000
```

### Step 2: DNS Setup (for subdomain routing)

**For Development:**
Add to `/etc/hosts` (Linux/Mac) or `C:\Windows\System32\drivers\etc\hosts` (Windows):
```
127.0.0.1 localhost tenant1.localhost tenant2.localhost admin.localhost
```

**For Production:**
Create wildcard DNS record:
```
*.yourdomain.com    A    YOUR_SERVER_IP
```

### Step 3: Deploy

Run the automated deployment script:
```bash
chmod +x scripts/deploy-multitenant.sh
./scripts/deploy-multitenant.sh
```

The script will:
- ✅ Check prerequisites
- ✅ Create database backups (if upgrading)
- ✅ Start all services
- ✅ Initialize default tenant
- ✅ Run health checks
- ✅ Display access information

### Step 4: Initial Setup

1. **Access Admin Panel:**
   ```
   URL: http://localhost:9000/admin
   ```

2. **Complete Initial Setup:**
   - Create admin user account
   - Configure basic settings
   - Test email configuration

3. **Create Your First Tenant:**
   ```bash
   curl -X POST http://localhost:9000/api/tenants \
     -H "Content-Type: application/json" \
     -H "Authorization: Bearer YOUR_ADMIN_TOKEN" \
     -d '{
       "name": "Acme Corporation",
       "slug": "acme", 
       "domain": "mail.acme.com"
     }'
   ```

## 🌐 Multi-Tenant Access Methods

### 1. Subdomain Access
```
# Admin/Default tenant
http://localhost:9000
http://admin.yourdomain.com

# Tenant-specific access  
http://acme.yourdomain.com
http://company2.yourdomain.com
```

### 2. Custom Domain
```
# Configure in tenant settings
http://mail.acme.com
http://newsletter.company2.com
```

### 3. Header-Based (API/Development)
```bash
curl -H "X-Tenant-ID: 2" http://localhost:9000/api/subscribers
```

## 🛠 Configuration Options

### Multi-Tenancy Settings

```env
# Enable/disable multi-tenancy
LISTMONK_TENANT_MODE=true

# Tenant resolution strategy
LISTMONK_TENANT_STRATEGY=subdomain  # subdomain|domain|header

# Domain suffix for subdomains
LISTMONK_TENANT_DOMAIN_SUFFIX=.yourdomain.com

# Default tenant ID (for fallback)
LISTMONK_DEFAULT_TENANT_ID=1

# Strict tenant enforcement
LISTMONK_TENANT_REQUIRE=false
```

### Database Settings

```env
# PostgreSQL Configuration
POSTGRES_DB=listmonk
POSTGRES_USER=listmonk
POSTGRES_PASSWORD=your-secure-password

# Database connection pool
LISTMONK_db__max_open=25
LISTMONK_db__max_idle=25
LISTMONK_db__max_lifetime=300s
```

### Performance Settings

```env
# Application performance
LISTMONK_app__concurrency=20
LISTMONK_app__message_rate=10
LISTMONK_app__batch_size=1000

# Cache settings
LISTMONK_app__cache_slow_queries=true
LISTMONK_app__cache_slow_queries_interval="0 3 * * *"
```

## 📊 Monitoring & Health Checks

### Health Check Endpoints

```bash
# Application health
curl http://localhost:9000/api/health

# Tenant system health
curl http://localhost:9000/api/health/tenants

# Database health
curl http://localhost:9000/api/health/db
```

### Service Monitoring

```bash
# View service status
docker-compose -f docker-compose.multitenant.yml ps

# View logs
docker-compose -f docker-compose.multitenant.yml logs -f

# View specific service logs
docker-compose -f docker-compose.multitenant.yml logs -f listmonk-app
```

## 🔧 Management Commands

### Docker Compose Commands

```bash
# Start services
docker-compose -f docker-compose.multitenant.yml up -d

# Stop services  
docker-compose -f docker-compose.multitenant.yml down

# Restart services
docker-compose -f docker-compose.multitenant.yml restart

# View service status
docker-compose -f docker-compose.multitenant.yml ps

# Update services
docker-compose -f docker-compose.multitenant.yml pull
docker-compose -f docker-compose.multitenant.yml up -d
```

### Tenant Management

```bash
# List all tenants (requires admin auth)
curl -H "Authorization: Bearer TOKEN" http://localhost:9000/api/tenants

# Create tenant
curl -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer TOKEN" \
  -d '{"name":"New Tenant","slug":"newtenant"}' \
  http://localhost:9000/api/tenants

# Get tenant stats
curl -H "Authorization: Bearer TOKEN" \
  http://localhost:9000/api/tenants/2/stats
```

### Database Management

```bash
# Access database console
docker exec -it listmonk-db psql -U listmonk -d listmonk

# Create database backup
docker exec listmonk-db pg_dump -U listmonk listmonk > backup.sql

# Restore database
docker exec -i listmonk-db psql -U listmonk -d listmonk < backup.sql

# Run tenant isolation tests
docker exec -i listmonk-db psql -U listmonk -d listmonk < test_tenant_isolation.sql
```

## 🚨 Troubleshooting

### Common Issues

#### 1. "Tenant not found" errors
```bash
# Check DNS resolution
nslookup tenant1.yourdomain.com

# Check tenant exists
curl -H "X-Tenant-ID: 1" http://localhost:9000/api/health/tenants

# Check logs
docker-compose -f docker-compose.multitenant.yml logs listmonk-app
```

#### 2. Database connection errors
```bash
# Check database status
docker exec listmonk-db pg_isready -U listmonk

# Check database logs
docker-compose -f docker-compose.multitenant.yml logs listmonk-db

# Restart database
docker-compose -f docker-compose.multitenant.yml restart listmonk-db
```

#### 3. Email sending issues
```bash
# Check SMTP configuration per tenant
curl -H "Authorization: Bearer TOKEN" \
  http://localhost:9000/api/tenants/1/settings | grep smtp

# Test email sending
curl -X POST -H "Content-Type: application/json" \
  -H "Authorization: Bearer TOKEN" \
  -d '{"subject":"Test","body":"Test email"}' \
  http://localhost:9000/api/campaigns/test
```

### Debug Mode

Enable debug logging:
```env
LISTMONK_app__log_level=debug
```

### Service Recovery

If services are unhealthy:
```bash
# Full restart
docker-compose -f docker-compose.multitenant.yml down
docker-compose -f docker-compose.multitenant.yml up -d

# Reset and rebuild
docker-compose -f docker-compose.multitenant.yml down -v
docker-compose -f docker-compose.multitenant.yml build --no-cache
docker-compose -f docker-compose.multitenant.yml up -d
```

## 🔄 Updates & Maintenance

### Updating Listmonk

```bash
# Create backup first
./scripts/deploy-multitenant.sh --backup

# Pull latest images
docker-compose -f docker-compose.multitenant.yml pull

# Restart with new images
docker-compose -f docker-compose.multitenant.yml up -d
```

### Regular Maintenance

**Daily:**
- Check service health
- Monitor disk usage
- Review error logs

**Weekly:**
- Create database backups
- Clean up old logs
- Update Docker images

**Monthly:**
- Review tenant usage
- Optimize database
- Update system packages

### Backup Strategy

```bash
# Automated backup script
#!/bin/bash
DATE=$(date +%Y%m%d-%H%M%S)
docker exec listmonk-db pg_dump -U listmonk listmonk > "backups/listmonk-$DATE.sql"
tar -czf "backups/uploads-$DATE.tar.gz" uploads/
find backups/ -name "*.sql" -mtime +30 -delete
find backups/ -name "*.tar.gz" -mtime +30 -delete
```

## 📚 Additional Resources

- **Full Documentation:** `MULTITENANCY_IMPLEMENTATION.md`
- **Setup Guide:** `DOCKER_MULTITENANT_SETUP.md` 
- **Integration Guide:** `TENANT_INTEGRATION_PATCH.md`
- **Testing:** `test_tenant_isolation.sql`

## 🆘 Getting Help

1. **Check Logs:** `docker-compose logs -f`
2. **Health Checks:** `curl http://localhost:9000/api/health/tenants`
3. **Database Console:** `docker exec -it listmonk-db psql -U listmonk -d listmonk`
4. **Issues:** Check GitHub issues or create new one

## 🎯 Quick Validation

Verify your deployment works:

```bash
# 1. Check all services running
docker-compose -f docker-compose.multitenant.yml ps

# 2. Test application
curl http://localhost:9000/api/health

# 3. Test tenant system  
curl http://localhost:9000/api/health/tenants

# 4. Test database
docker exec listmonk-db psql -U listmonk -d listmonk -c "SELECT COUNT(*) FROM tenants;"

# 5. Access admin panel
open http://localhost:9000/admin
```

✅ **Success!** Your multi-tenant Listmonk is now running!