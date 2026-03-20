#!/bin/bash
set -e

# Install Go
curl -LO https://go.dev/dl/go1.25.6.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.25.6.linux-amd64.tar.gz
rm go1.25.6.linux-amd64.tar.gz

# Add Go to system-wide PATH and set GOROOT
cat >> /etc/profile << 'EOF'
export GOROOT=/usr/local/go
export GOPATH=$HOME/go
export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin
EOF

# Verify Go installation
GOROOT=/usr/local/go /usr/local/go/bin/go version

# Enable universe repository and install Caddy
apt-get update
apt-get install -y software-properties-common
add-apt-repository -y universe
apt-get update
apt-get install -y caddy

# Disable the default Caddy service - we'll use our own config
systemctl disable caddy

# Create the sandfire config directory
mkdir -p /etc/sandfire

# Create empty services file (other layers can append to this)
touch /etc/sandfire/services

# Create the Caddyfile generator script
cat > /usr/local/bin/sandfire-generate-caddyfile.sh << 'SCRIPT'
#!/bin/bash
set -e

SERVICES_FILE="/etc/sandfire/services"
CADDYFILE="/etc/caddy/Caddyfile"
INDEX_DIR="/var/www/html"

mkdir -p "$INDEX_DIR"

# Start building the Caddyfile
cat > "$CADDYFILE" << 'HEADER'
# Auto-generated Caddyfile - do not edit manually
# Edit /etc/sandfire/services instead

:80 {
HEADER

# Check if services file exists and has content
if [ -s "$SERVICES_FILE" ]; then
    # Update /etc/hosts with service.localhost entries
    # First, remove any previous sandfire-managed entries
    sed -i '/# sandfire-managed$/d' /etc/hosts

    # Add new entries for each service
    while read -r name port || [ -n "$name" ]; do
        # Skip empty lines and comments
        [[ -z "$name" || "$name" =~ ^# ]] && continue
        echo "127.0.0.1   ${name}.localhost # sandfire-managed" >> /etc/hosts
    done < "$SERVICES_FILE"

    # Read services and generate reverse proxy rules (sorted alphabetically)
    SERVICES_HTML=""

    while read -r name port || [ -n "$name" ]; do
        # Skip empty lines and comments
        [[ -z "$name" || "$name" =~ ^# ]] && continue

        # Generate matcher and handler for this service
        # Using expression matcher to check if host starts with service name
        cat >> "$CADDYFILE" << EOF
    @${name} expression {http.request.host}.startsWith("${name}.")
    handle @${name} {
        reverse_proxy localhost:${port} {
            flush_interval -1
        }
    }

EOF

        # Build HTML list item (data-service attribute for JS to process)
        SERVICES_HTML="${SERVICES_HTML}        <li><a href=\"#\" data-service=\"${name}\">${name}</a> &rarr; port ${port}</li>\n"
    done < <(sort "$SERVICES_FILE")

    # Generate index page with service list
    cat > "$INDEX_DIR/index.html" << EOF
<!DOCTYPE html>
<html>
<head>
    <title>Sandfire VM - Services</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 600px; margin: 50px auto; padding: 20px; }
        h1 { color: #333; }
        ul { list-style: none; padding: 0; }
        li { padding: 10px; margin: 5px 0; background: #f5f5f5; border-radius: 4px; }
        a { color: #0066cc; text-decoration: none; font-weight: bold; }
        a:hover { text-decoration: underline; }
        code { background: #eee; padding: 2px 6px; border-radius: 3px; }
    </style>
</head>
<body>
    <h1>Available Services</h1>
    <ul>
$(echo -e "$SERVICES_HTML")
    </ul>
    <p><small>Services are defined in <code>/etc/sandfire/services</code></small></p>
    <script>
        document.querySelectorAll('a[data-service]').forEach(function(a) {
            var service = a.getAttribute('data-service');
            a.href = location.protocol + '//' + service + '.' + location.host;
        });
    </script>
</body>
</html>
EOF

else
    # No services defined - show info page
    cat > "$INDEX_DIR/index.html" << 'EOF'
<!DOCTYPE html>
<html>
<head>
    <title>Sandfire VM - No Services</title>
    <style>
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 600px; margin: 50px auto; padding: 20px; }
        h1 { color: #333; }
        code { background: #eee; padding: 2px 6px; border-radius: 3px; }
        pre { background: #f5f5f5; padding: 15px; border-radius: 4px; overflow-x: auto; }
    </style>
</head>
<body>
    <h1>No Services Defined</h1>
    <p>This VM has no services configured yet.</p>
    <p>To add services, edit <code>/etc/sandfire/services</code> with entries in the format:</p>
    <pre>service_name port</pre>
    <p>For example:</p>
    <pre>api 3000
app 8000</pre>
    <p>Then restart the VM or run:</p>
    <pre>sudo systemctl restart sandfire-generate-caddyfile
sudo systemctl restart caddy</pre>
</body>
</html>
EOF
fi

# Add default handler to serve the index page
cat >> "$CADDYFILE" << 'FOOTER'
    # Default handler - serve the index page
    handle {
        root * /var/www/html
        file_server
    }
}
FOOTER

echo "Caddyfile generated successfully"
SCRIPT

chmod +x /usr/local/bin/sandfire-generate-caddyfile.sh

# Create systemd service to generate Caddyfile before Caddy starts
cat > /etc/systemd/system/sandfire-generate-caddyfile.service << 'EOF'
[Unit]
Description=Generate Caddyfile from services configuration
Before=caddy.service
After=network.target

[Service]
Type=oneshot
ExecStart=/usr/local/bin/sandfire-generate-caddyfile.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

# Enable both services
systemctl enable sandfire-generate-caddyfile
systemctl enable caddy

# Generate initial Caddyfile
/usr/local/bin/sandfire-generate-caddyfile.sh

# Create empty config-templates file (other layers can append to this)
touch /etc/sandfire/config-templates

# Create the config templating script
# This processes config files with #sandfire:template= markers, replacing {{DOMAIN}}
# with the actual domain from /etc/sandfire/domain
cat > /usr/local/bin/sandfire-template-config.sh << 'SCRIPT'
#!/bin/bash
# Regenerates config values marked with #sandfire:template= comments
# Format: KEY = "old_value" #sandfire:template=template_pattern
# Result: KEY = "new_value" #sandfire:template=template_pattern

set -e

DOMAIN_FILE="/etc/sandfire/domain"
CONFIG_LIST="/etc/sandfire/config-templates"

if [ ! -f "$DOMAIN_FILE" ]; then
    echo "Warning: No domain file found at $DOMAIN_FILE"
    exit 0
fi

DOMAIN=$(cat "$DOMAIN_FILE")
if [ -z "$DOMAIN" ]; then
    echo "Warning: Domain file is empty"
    exit 0
fi

if [ ! -f "$CONFIG_LIST" ]; then
    echo "No config templates file found"
    exit 0
fi

while read -r config_path || [ -n "$config_path" ]; do
    [[ -z "$config_path" || "$config_path" =~ ^# ]] && continue
    if [ ! -f "$config_path" ]; then
        echo "Warning: Config file not found: $config_path"
        continue
    fi

    echo "Processing: $config_path"

    # Capture original ownership before modifying
    original_owner=$(stat -c '%U:%G' "$config_path" 2>/dev/null || echo "")

    # Process lines with #sandfire:template= marker
    tmp_file=$(mktemp)
    while IFS= read -r line || [ -n "$line" ]; do
        if [[ "$line" =~ ^([^=]+)=.*\#sandfire:template=(.+)$ ]]; then
            key="${BASH_REMATCH[1]}"
            template="${BASH_REMATCH[2]}"
            # Replace {{DOMAIN}} in template with actual domain
            new_value="${template//\{\{DOMAIN\}\}/$DOMAIN}"
            echo "${key}= ${new_value} #sandfire:template=${template}"
        else
            echo "$line"
        fi
    done < "$config_path" > "$tmp_file"

    mv "$tmp_file" "$config_path"

    # Restore original ownership
    if [ -n "$original_owner" ]; then
        chown "$original_owner" "$config_path" 2>/dev/null || true
    fi

done < "$CONFIG_LIST"

echo "Config templating complete for domain: $DOMAIN"
SCRIPT

chmod +x /usr/local/bin/sandfire-template-config.sh

# Create systemd service for config templating
# This runs after MMDS fetches the domain, but before application services start
cat > /etc/systemd/system/sandfire-template-config.service << 'EOF'
[Unit]
Description=Template config files with VM domain
After=sandfire-mmds-motd.service
Wants=sandfire-mmds-motd.service

[Service]
Type=oneshot
ExecStart=/usr/local/bin/sandfire-template-config.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl enable sandfire-template-config

# Teach claude how to find the domain
cat > /home/sandfire/.claude/CLAUDE.md << 'EOF'
This environment runs in a Sandfire microVM with a domain pointed to it.

Since this an isolated microVM, you have full control. You may run whatever commands you want, and install whatever tools you need. You have sudo permission.

Do not worry about asking the user for permission - act autonomously and with agency.

To find the URL for a service:
1. Read the base domain from `/etc/sandfire/domain`
2. Prepend the service name with a dot: `{service}.{domain}`

Example: if domain is `vm.example.com` and service is `app`, the URL is `https://app.vm.example.com`

Service names for each project are specified in that project's CLAUDE.md.

If you are creating a new webserver, you should add it as a service so that the user can access it via the domain mentioned above. Do not replace the main Caddy webserver or its configuration unless the user specifically asks for it.

To add a new service:
1. Decide on a name and port for the service.
2. Append a new line to /etc/sandfire/services like so: `{name} {port}`
3. Run `sudo systemctl restart sandfire-generate-caddyfile`
4. Run `sudo systemctl restart caddy`
EOF
chown sandfire:sandfire /home/sandfire/.claude/CLAUDE.md

# Create systemd service for OpenCode web UI (not enabled by default)
cat > /etc/systemd/system/opencode-web.service << 'EOF'
[Unit]
Description=OpenCode Web UI
After=network.target

[Service]
Type=simple
User=sandfire
Group=sandfire
Environment=DISPLAY=:0
WorkingDirectory=/home/sandfire/workspace
ExecStart=/home/sandfire/.opencode/bin/opencode serve
Restart=on-failure

[Install]
WantedBy=multi-user.target
EOF

# Create toggle script for OpenCode web UI
cat > /usr/local/bin/sandfire-oc << 'SCRIPT'
#!/bin/bash
set -e

# Re-exec as root if not already
if [ "$(id -u)" -ne 0 ]; then
    exec sudo "$0" "$@"
fi

SERVICES_FILE="/etc/sandfire/services"
SERVICE_NAME="oc"
SERVICE_PORT="4096"
SYSTEMD_UNIT="opencode-web.service"

case "$1" in
    on)
        # Add service registration if not already present
        if ! grep -q "^${SERVICE_NAME} " "$SERVICES_FILE" 2>/dev/null; then
            echo "${SERVICE_NAME} ${SERVICE_PORT}" >> "$SERVICES_FILE"
        fi
        # Enable and start the service
        systemctl enable "$SYSTEMD_UNIT" &>/dev/null
        systemctl start "$SYSTEMD_UNIT"
        # Regenerate Caddyfile and reload Caddy
        systemctl restart sandfire-generate-caddyfile
        systemctl restart caddy
        DOMAIN=$(cat /etc/sandfire/domain 2>/dev/null)
        echo "OpenCode web UI enabled on https://${SERVICE_NAME}.${DOMAIN}"
        ;;
    off)
        # Stop and disable the service
        systemctl stop "$SYSTEMD_UNIT" 2>/dev/null || true
        systemctl disable "$SYSTEMD_UNIT" 2>/dev/null || true
        # Remove service registration
        sed -i "/^${SERVICE_NAME} /d" "$SERVICES_FILE"
        # Regenerate Caddyfile and reload Caddy
        systemctl restart sandfire-generate-caddyfile
        systemctl restart caddy
        echo "OpenCode web UI disabled"
        ;;
    *)
        echo "Usage: sandfire-oc {on|off}"
        exit 1
        ;;
esac
SCRIPT
chmod +x /usr/local/bin/sandfire-oc

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
