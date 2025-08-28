# 🚀 Multi-Tenant Listmonk Deployment Instructions

## **What We've Built**

Your fork now contains a complete multi-tenant Listmonk implementation that needs to be built from source. Here's what's ready:

### **✅ Files Created/Modified:**
- `Dockerfile.build` - Custom Dockerfile that builds from your fork's source code
- `docker-compose.multitenant.yml` - Updated to build custom image
- `scripts/deploy-multitenant.sh` - Updated deployment script with build process
- `scripts/build-multitenant.sh` - Standalone build script (optional)
- All multi-tenancy Go code integration files in `cmd/`, `internal/`, `models/`
- Updated `queries.sql` with 1300+ tenant-aware queries
- Database migration: `migrations/001_add_multitenancy.sql`

---

## **🎯 Quick Deployment (On Your Server)**

### **1. Pull Latest Fork Changes**
```bash
cd /opt/listmonk
git pull origin master  # or your branch name
```

### **2. Stop Existing Containers (if running)**
```bash
sudo docker compose -f docker-compose.multitenant.yml down
```

### **3. Configure Environment**
```bash
# Copy and edit environment file
cp .env.multitenant.sample .env

# Edit with your settings (REQUIRED!)
sudo nano .env
```

**Key settings to update in `.env`:**
```env
# Change these required settings:
LISTMONK_ADMIN_PASSWORD=your-secure-password
LISTMONK_SUPER_ADMIN_EMAIL=your-email@domain.com

# For production, also update:
LISTMONK_TENANT_BASE_DOMAIN=yourdomain.com
LISTMONK_TENANT_DOMAIN_SUFFIX=.yourdomain.com
```

### **4. Deploy with Build**
```bash
# Make script executable (if needed)
chmod +x scripts/deploy-multitenant.sh

# Run deployment (this will build custom image)
./scripts/deploy-multitenant.sh
```

---

## **🔧 What the Deploy Script Does Now**

The updated deploy script will:

1. **Check Prerequisites** - Verify all files exist
2. **Fix Permissions** - Handle file ownership issues  
3. **Build Custom Image** - Compile Go app with multi-tenancy code
4. **Apply Database Migration** - Add tenant tables and RLS policies
5. **Start All Services** - Launch app, database, redis
6. **Run Health Checks** - Verify everything works
7. **Display Access Info** - Show URLs and next steps

---

## **🏗️ Build Process Details**

### **Custom Docker Build**
The `Dockerfile.build` will:
- Use Go 1.21 to compile your fork's source code
- Include all our multi-tenancy modifications
- Build frontend assets (if needed)
- Create optimized production image
- Include tenant-aware `queries.sql`

### **Build Command** 
```bash
# Manual build (if needed)
sudo docker compose -f docker-compose.multitenant.yml build --no-cache
```

---

## **🔍 Expected Output**

After successful deployment, you should see:

```
================================
  LISTMONK MULTI-TENANT SETUP
================================

Application URL: http://localhost:9000
Admin Panel: http://localhost:9000/admin
Health Check: http://localhost:9000/api/health/tenants

Docker Services:
NAME                IMAGE                           STATUS
listmonk_app_mt     listmonk-multitenant:latest    Up (healthy)
listmonk_db_mt      postgres:17-alpine              Up (healthy)  
listmonk_redis_mt   redis:7-alpine                  Up (healthy)
```

---

## **🎯 Testing Multi-Tenancy**

After deployment:

1. **Access Admin Panel**: http://your-server:9000/admin
2. **Login with admin credentials** from your `.env` file  
3. **Look for tenant management** in the admin interface
4. **Create your first tenant** 
5. **Test tenant isolation** by accessing different subdomains

---

## **🐛 Troubleshooting**

### **Build Fails**
```bash
# Check build logs
sudo docker compose -f docker-compose.multitenant.yml logs app

# Clean build
sudo docker system prune -f
sudo docker compose -f docker-compose.multitenant.yml build --no-cache
```

### **Database Connection Issues**  
```bash
# Check database logs
sudo docker logs listmonk_db_mt

# Verify migration applied
sudo docker exec listmonk_db_mt psql -U listmonk -d listmonk -c "SELECT * FROM tenants LIMIT 1;"
```

### **App Won't Start**
```bash
# Check application logs
sudo docker logs listmonk_app_mt --tail 50

# Verify custom image was built
sudo docker images | grep listmonk-multitenant
```

---

## **📁 Important Files**

- **Deploy Script**: `scripts/deploy-multitenant.sh` - Main deployment automation
- **Docker Build**: `Dockerfile.build` - Custom image build process  
- **Environment**: `.env` - Your configuration (edit this!)
- **Migration**: `migrations/001_add_multitenancy.sql` - Database schema changes
- **Queries**: `queries.sql` - Tenant-aware database queries

---

## **🎊 Next Steps After Successful Deploy**

1. **Create tenants** via admin panel
2. **Set up DNS** for subdomain routing (optional)
3. **Configure SSL** for production (recommended)
4. **Set up backups** using included scripts
5. **Monitor logs** and performance

---

## **🆘 Support**

If deployment fails:

1. **Check logs**: `sudo docker logs listmonk_app_mt`  
2. **Verify environment**: Ensure `.env` is properly configured
3. **Check permissions**: Run `./scripts/fix-permissions.sh` if needed
4. **Rebuild**: Try `sudo docker compose -f docker-compose.multitenant.yml build --no-cache`

**The key difference**: We're now building a custom image from your fork's source code instead of using the standard Listmonk image. This includes all our multi-tenancy modifications! 🚀