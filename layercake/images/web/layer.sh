#!/bin/bash
set -e

# Install and configure nginx web server

apt-get update
apt-get install -y nginx

# Enable nginx to start on boot
systemctl enable nginx

# Create a simple default page
cat > /var/www/html/index.html << 'EOF'
<!DOCTYPE html>
<html>
<head>
    <title>Sandfire VM</title>
</head>
<body>
    <h1>Welcome to Sandfire VM</h1>
    <p>Nginx is running successfully.</p>
</body>
</html>
EOF

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
