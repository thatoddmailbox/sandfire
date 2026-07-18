#!/bin/bash
set -e

# Install Go
curl -LO https://go.dev/dl/go1.26.4.linux-amd64.tar.gz
rm -rf /usr/local/go
tar -C /usr/local -xzf go1.26.4.linux-amd64.tar.gz
rm go1.26.4.linux-amd64.tar.gz

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

# -----------------------------------------------------------------------------
# sandfire-files: static workspace browser with client-side Markdown rendering
# (off by default; mirrors the sandfire-oc toggle pattern above)
# -----------------------------------------------------------------------------

# Build the self-contained Markdown render shell (render.html).
# Markdown is rendered CLIENT-SIDE: the shell fetches the raw file and renders
# it in the browser via inlined marked.js + highlight.js. This is immune to file
# content (a file containing {{ }} can't trigger server-side template execution)
# and works on the Caddy shipped by this layer without needing readFile (2.7+).
#
# The libraries are pinned to immutable versioned jsDelivr URLs and verified by
# sha256, so a changed/compromised CDN fails the build instead of baking bad
# bytes. Downloaded into a temp build dir; only the generated render.html ships.
FILES_ASSETS="/etc/caddy/files-assets"
FILES_BUILD="$(mktemp -d)"

curl -fsSL -o "$FILES_BUILD/marked.min.js"        "https://cdn.jsdelivr.net/npm/marked@12.0.2/marked.min.js"
curl -fsSL -o "$FILES_BUILD/highlight.min.js"     "https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.9.0/build/highlight.min.js"
curl -fsSL -o "$FILES_BUILD/hljs-github.css"      "https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.9.0/build/styles/github.min.css"
curl -fsSL -o "$FILES_BUILD/hljs-github-dark.css" "https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.9.0/build/styles/github-dark.min.css"

sha256sum -c - <<SUMS
15fabce5b65898b32b03f5ed25e9f891a729ad4c0d6d877110a7744aa847a894  $FILES_BUILD/marked.min.js
837a6fa5b0c736b52bbde2b2b6190f305da3fc9ed41681db5321507057b5c846  $FILES_BUILD/highlight.min.js
3a9a5def8b9c311e5ae43abde85c63133185eed4f0d9f67fea4b00a8308cf066  $FILES_BUILD/hljs-github.css
9f208d022102b1d0c7aebfecd8e42ca7997d5de636649d2b31ea63093d809019  $FILES_BUILD/hljs-github-dark.css
SUMS

mkdir -p "$FILES_ASSETS"
{
    cat << 'HTML_HEAD'
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Loading…</title>
<style>
:root {
  --bg:#fff; --fg:#1f2328; --muted:#59636e; --border:#d1d9e0;
  --bar-bg:#f6f8fa; --link:#0969da; --code-bg:#f6f8fa; --code-border:#d1d9e0;
  --quote-fg:#59636e; --quote-border:#d1d9e0; --table-alt:#f6f8fa;
}
@media (prefers-color-scheme: dark){
  :root{
    --bg:#0d1117; --fg:#e6edf3; --muted:#9198a1; --border:#3d444d;
    --bar-bg:#161b22; --link:#4493f8; --code-bg:#161b22; --code-border:#3d444d;
    --quote-fg:#9198a1; --quote-border:#3d444d; --table-alt:#161b22;
  }
}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);
  font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Noto Sans",Helvetica,Arial,sans-serif;
  font-size:16px;line-height:1.6}
.topbar{position:sticky;top:0;z-index:10;display:flex;align-items:center;gap:16px;
  flex-wrap:wrap;padding:10px 16px;background:var(--bar-bg);
  border-bottom:1px solid var(--border);font-size:14px}
.topbar .path{color:var(--muted);font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;word-break:break-all}
.topbar .spacer{flex:1 1 auto}
.topbar a{color:var(--link);text-decoration:none;white-space:nowrap}
.topbar a:hover{text-decoration:underline}
.content{max-width:900px;margin:0 auto;padding:32px 24px 96px}
.content h1,.content h2{padding-bottom:.3em;border-bottom:1px solid var(--border)}
.content h1,.content h2,.content h3,.content h4{margin-top:1.5em;margin-bottom:.6em;line-height:1.25}
.content h1:first-child{margin-top:0}
.content a{color:var(--link);text-decoration:none}
.content a:hover{text-decoration:underline}
.content code{background:var(--code-bg);border:1px solid var(--code-border);
  padding:.15em .4em;border-radius:6px;font-size:85%;
  font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
.content pre{background:var(--code-bg);border:1px solid var(--code-border);
  padding:16px;border-radius:6px;overflow-x:auto;line-height:1.45}
.content pre code{background:none;border:0;padding:0;font-size:85%}
.content blockquote{margin:0 0 16px;padding:0 1em;color:var(--quote-fg);
  border-left:.25em solid var(--quote-border)}
.content table{border-collapse:collapse;display:block;overflow-x:auto;margin-bottom:16px}
.content th,.content td{border:1px solid var(--border);padding:6px 13px}
.content tr:nth-child(2n){background:var(--table-alt)}
.content img{max-width:100%}
.content hr{border:0;border-top:1px solid var(--border);margin:24px 0}
.loading{color:var(--muted)}
</style>
<style>
HTML_HEAD

    cat "$FILES_BUILD/hljs-github.css"

    cat << 'HTML_DARK_OPEN'
</style>
<style>
@media (prefers-color-scheme: dark){
HTML_DARK_OPEN

    cat "$FILES_BUILD/hljs-github-dark.css"

    cat << 'HTML_BODY'
}
</style>
</head>
<body>
<div class="topbar">
  <span class="path" id="path"></span>
  <span class="spacer"></span>
  <a href="./" title="Browse the containing folder">📁 Folder</a>
  <a href="/" title="Browse the workspace root">🏠 Root</a>
  <a id="raw" title="Download the raw file">⬇ Raw</a>
</div>
<article id="content" class="content loading">Loading…</article>
<script>
HTML_BODY

    cat "$FILES_BUILD/marked.min.js"

    printf '\n</script>\n<script>\n'

    cat "$FILES_BUILD/highlight.min.js"

    cat << 'HTML_RUNTIME'

</script>
<script>
(function(){
  var rawPath = location.pathname;
  var pretty = decodeURIComponent(rawPath);
  document.getElementById('path').textContent = pretty;
  document.getElementById('raw').href = '/_raw' + rawPath;
  var base = pretty.split('/').pop();
  document.title = base || pretty;
  var content = document.getElementById('content');
  fetch('/_rawtext' + rawPath)
    .then(function(r){ if(!r.ok) throw new Error('HTTP ' + r.status); return r.text(); })
    .then(function(md){
      content.classList.remove('loading');
      content.innerHTML = marked.parse(md);
      content.querySelectorAll('pre code').forEach(function(el){
        try { hljs.highlightElement(el); } catch(e){}
      });
    })
    .catch(function(e){
      content.textContent = 'Failed to load file: ' + e.message;
    });
})();
</script>
</body>
</html>
HTML_RUNTIME
} > "$FILES_ASSETS/render.html"

# Only the caddy process needs to read the assets.
chown -R caddy:caddy "$FILES_ASSETS"
rm -rf "$FILES_BUILD"

# Dedicated Caddy instance config (separate from the main auto-generated Caddyfile).
# admin off  -> don't share/steal the main Caddy's admin socket (localhost:2019).
# auto_https off -> TLS is terminated by the main Caddy reverse-proxying files.<domain>.
cat > /etc/caddy/sandfire-files.Caddyfile << 'EOF'
{
	admin off
	auto_https off
}

:8088 {
	# Public raw download: forces the browser to download untouched bytes
	handle_path /_raw/* {
		root * /home/sandfire/workspace
		header Content-Disposition attachment
		file_server
	}

	# Raw text: consumed by render.html's fetch(); served as-is (no download header)
	handle_path /_rawtext/* {
		root * /home/sandfire/workspace
		file_server
	}

	# Markdown: *.md / *.markdown -> static client-side render shell
	@md path *.md *.markdown
	handle @md {
		root * /etc/caddy/files-assets
		rewrite * /render.html
		file_server
	}

	# Everything else: browsable directory listings + raw static files
	handle {
		root * /home/sandfire/workspace
		file_server browse
	}
}
EOF

# Systemd unit - ships DISABLED (no WantedBy symlink until the toggle enables it),
# so it never starts at boot on a fresh image.
cat > /etc/systemd/system/sandfire-files.service << 'EOF'
[Unit]
Description=Sandfire Files - static file browser with Markdown rendering
After=network.target

[Service]
Type=simple
User=sandfire
Group=sandfire
Environment=HOME=/home/sandfire
Environment=XDG_DATA_HOME=/home/sandfire/.local/share
Environment=XDG_CONFIG_HOME=/home/sandfire/.config
ExecStart=/usr/bin/caddy run --config /etc/caddy/sandfire-files.Caddyfile --adapter caddyfile
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
EOF
# NOTE: deliberately NOT `systemctl enable`d - the base image ships it disabled.

# Toggle script (on|off|status) - mirrors sandfire-oc.
cat > /usr/local/bin/sandfire-files << 'SCRIPT'
#!/bin/bash
set -e
if [ "$(id -u)" -ne 0 ]; then exec sudo "$0" "$@"; fi
SERVICES_FILE="/etc/sandfire/services"
SERVICE_NAME="files"
SERVICE_PORT="8088"
SYSTEMD_UNIT="sandfire-files.service"
case "$1" in
    on)
        if ! grep -q "^${SERVICE_NAME} " "$SERVICES_FILE" 2>/dev/null; then
            echo "${SERVICE_NAME} ${SERVICE_PORT}" >> "$SERVICES_FILE"
        fi
        systemctl enable "$SYSTEMD_UNIT" &>/dev/null
        systemctl start "$SYSTEMD_UNIT"
        systemctl restart sandfire-generate-caddyfile
        systemctl restart caddy
        DOMAIN=$(cat /etc/sandfire/domain 2>/dev/null)
        echo "Files browser enabled on https://${SERVICE_NAME}.${DOMAIN}"
        echo "WARNING: this exposes your ENTIRE workspace (including dotfiles, .git,"
        echo "         .env, credentials, .claude/) UNAUTHENTICATED to anyone who can"
        echo "         reach that URL. Run 'sandfire-files off' when you're done."
        ;;
    off)
        systemctl stop "$SYSTEMD_UNIT" 2>/dev/null || true
        systemctl disable "$SYSTEMD_UNIT" 2>/dev/null || true
        sed -i "/^${SERVICE_NAME} /d" "$SERVICES_FILE"
        systemctl restart sandfire-generate-caddyfile
        systemctl restart caddy
        echo "Files browser disabled"
        ;;
    status) systemctl status "$SYSTEMD_UNIT" --no-pager || true ;;
    *) echo "Usage: sandfire-files {on|off|status}"; exit 1 ;;
esac
SCRIPT
chmod +x /usr/local/bin/sandfire-files

# Clean up
rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*
