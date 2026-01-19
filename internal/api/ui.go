package api

import "net/http"

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Sandfire VM Manager</title>
    <style>
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: #f5f5f5;
            color: #333;
            min-height: 100vh;
            padding: 20px;
        }
        h1 {
            color: #d4470a;
            margin-bottom: 20px;
            display: flex;
            align-items: center;
            gap: 10px;
        }
        h1::before {
            content: "";
            display: inline-block;
            width: 8px;
            height: 8px;
            background: #d4470a;
            border-radius: 50%;
            animation: pulse 2s infinite;
        }
        @keyframes pulse {
            0%, 100% { opacity: 1; }
            50% { opacity: 0.5; }
        }
        .container { max-width: 1000px; margin: 0 auto; }
        .section {
            background: #fff;
            border-radius: 8px;
            padding: 20px;
            margin-bottom: 20px;
            box-shadow: 0 1px 3px rgba(0,0,0,0.1);
        }
        .section h2 {
            color: #d4470a;
            margin-bottom: 15px;
            font-size: 1.1em;
        }
        table {
            width: 100%;
            border-collapse: collapse;
        }
        th, td {
            padding: 12px;
            text-align: left;
            border-bottom: 1px solid #e5e5e5;
            vertical-align: middle;
        }
        th { color: #666; font-weight: 500; font-size: 0.85em; text-transform: uppercase; }
        tr:hover { background: #fafafa; }
        .state {
            display: inline-block;
            padding: 4px 10px;
            border-radius: 4px;
            font-size: 0.85em;
            font-weight: 500;
        }
        .state-running { background: #dcfce7; color: #166534; }
        .state-stopped { background: #fee2e2; color: #991b1b; }
        .state-error { background: #fef3c7; color: #92400e; }
        .btn {
            padding: 6px 14px;
            border: none;
            border-radius: 4px;
            cursor: pointer;
            font-size: 0.85em;
            font-weight: 500;
            transition: opacity 0.2s;
        }
        .btn:hover { opacity: 0.85; }
        .btn:disabled { opacity: 0.5; cursor: not-allowed; }
        .btn-start { background: #22c55e; color: #fff; }
        .btn-stop { background: #ef4444; color: #fff; }
        .btn-delete { background: #6b7280; color: #fff; }
        .btn-create { background: #d4470a; color: #fff; padding: 10px 20px; }
        .btn-copy { background: #3b82f6; color: #fff; }
        .actions { display: flex; gap: 8px; align-items: center; }
        .form-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
            gap: 15px;
        }
        .form-group { display: flex; flex-direction: column; gap: 5px; }
        .form-group label { color: #666; font-size: 0.85em; }
        .form-group input, .form-group select {
            padding: 10px;
            border: 1px solid #ddd;
            border-radius: 4px;
            background: #fff;
            color: #333;
            font-size: 0.95em;
        }
        .form-group input:focus, .form-group select:focus {
            outline: none;
            border-color: #d4470a;
        }
        .checkbox-group {
            flex-direction: row;
            align-items: center;
            gap: 10px;
        }
        .checkbox-group input { width: auto; }
        .form-actions { margin-top: 15px; }
        .ip-address { font-family: monospace; color: #166534; }
        .no-ip { color: #999; }
        .empty-state {
            text-align: center;
            padding: 40px;
            color: #999;
        }
        .error-msg {
            background: #fee2e2;
            color: #991b1b;
            padding: 10px 15px;
            border-radius: 4px;
            margin-bottom: 15px;
        }
        .vm-specs {
            color: #666;
            font-size: 0.85em;
        }
        .loading { opacity: 0.5; }
    </style>
</head>
<body>
    <div class="container">
        <h1>Sandfire</h1>

        <div id="error-container"></div>

        <div class="section">
            <h2>Virtual Machines</h2>
            <div id="vm-list">
                <div class="empty-state">Loading...</div>
            </div>
        </div>

        <div class="section">
            <h2>Create New VM</h2>
            <form id="create-form">
                <div class="form-grid">
                    <div class="form-group">
                        <label for="name">Name</label>
                        <input type="text" id="name" name="name" required placeholder="my-vm">
                    </div>
                    <div class="form-group">
                        <label for="os_image_id">OS Image</label>
                        <select id="os_image_id" name="os_image_id" required>
                            <option value="">Select an image...</option>
                        </select>
                    </div>
                    <div class="form-group">
                        <label for="ram_mb">RAM (MB)</label>
                        <input type="number" id="ram_mb" name="ram_mb" value="1024" min="128" step="128">
                    </div>
                    <div class="form-group">
                        <label for="disk_size_gb">Disk (GB)</label>
                        <input type="number" id="disk_size_gb" name="disk_size_gb" value="8" min="1">
                    </div>
                    <div class="form-group">
                        <label for="vcpu_count">vCPUs</label>
                        <input type="number" id="vcpu_count" name="vcpu_count" value="1" min="1" max="8">
                    </div>
                    <div class="form-group checkbox-group">
                        <input type="checkbox" id="internet_enabled" name="internet_enabled" checked>
                        <label for="internet_enabled">Internet Access</label>
                    </div>
                </div>
                <div class="form-actions">
                    <button type="submit" class="btn btn-create">Create VM</button>
                </div>
            </form>
        </div>
    </div>

    <script>
        const API = '/api';
        let osImageMap = {};

        async function fetchJSON(url, options = {}) {
            const res = await fetch(url, {
                headers: { 'Content-Type': 'application/json' },
                ...options
            });
            if (!res.ok) {
                const err = await res.json().catch(() => ({ error: res.statusText }));
                throw new Error(err.error || 'Request failed');
            }
            if (res.status === 204) return null;
            return res.json();
        }

        function showError(msg) {
            const container = document.getElementById('error-container');
            container.innerHTML = '<div class="error-msg">' + escapeHtml(msg) + '</div>';
            setTimeout(() => container.innerHTML = '', 5000);
        }

        function escapeHtml(text) {
            const div = document.createElement('div');
            div.textContent = text;
            return div.innerHTML;
        }

        async function loadOSImages() {
            try {
                const images = await fetchJSON(API + '/os-images');
                const select = document.getElementById('os_image_id');
                select.innerHTML = '<option value="">Select an image...</option>';
                osImageMap = {};
                images.forEach(img => {
                    osImageMap[img.id] = img.name;
                    const opt = document.createElement('option');
                    opt.value = img.id;
                    opt.textContent = img.name + ' (' + img.id + ')';
                    select.appendChild(opt);
                });
            } catch (e) {
                showError('Failed to load OS images: ' + e.message);
            }
        }

        async function loadVMs() {
            try {
                const vms = await fetchJSON(API + '/vms');
                renderVMs(vms);
            } catch (e) {
                showError('Failed to load VMs: ' + e.message);
            }
        }

        function renderVMs(vms) {
            const container = document.getElementById('vm-list');
            if (!vms || vms.length === 0) {
                container.innerHTML = '<div class="empty-state">No virtual machines yet. Create one below.</div>';
                return;
            }

            let html = '<table><thead><tr>';
            html += '<th>Name</th><th>State</th><th>IP Address</th><th>OS Image</th><th>Specs</th><th>Actions</th>';
            html += '</tr></thead><tbody>';

            vms.forEach(vm => {
                const stateClass = 'state-' + vm.state;
                const ip = vm.ip_address
                    ? '<span class="ip-address">' + escapeHtml(vm.ip_address) + '</span>'
                    : '<span class="no-ip">-</span>';
                const specs = vm.vcpu_count + ' vCPU, ' + vm.ram_mb + ' MB RAM, ' + vm.disk_size_gb + ' GB';
                const osImage = osImageMap[vm.os_image_id] || vm.os_image_id;

                html += '<tr data-id="' + escapeHtml(vm.id) + '">';
                html += '<td><strong>' + escapeHtml(vm.name) + '</strong><br><small style="color:#999"><a href="https://' + escapeHtml(vm.id) + '.' + window.location.hostname + '" target="_blank" style="color:#999;text-decoration:none">' + escapeHtml(vm.id) + '</a></small></td>';
                html += '<td><span class="state ' + stateClass + '">' + escapeHtml(vm.state) + '</span></td>';
                html += '<td>' + ip + '</td>';
                html += '<td><span class="vm-specs">' + escapeHtml(osImage) + '</span></td>';
                html += '<td><span class="vm-specs">' + escapeHtml(specs) + '</span></td>';
                html += '<td><div class="actions">';
                html += '<button class="btn btn-copy" onclick="copySSH(\'' + vm.id + '\', this)">Copy SSH</button>';

                if (vm.state === 'running') {
                    html += '<button class="btn btn-stop" onclick="stopVM(\'' + vm.id + '\')">Stop</button>';
                } else if (vm.state === 'stopped' || vm.state === 'error') {
                    html += '<button class="btn btn-start" onclick="startVM(\'' + vm.id + '\')">Start</button>';
                    html += '<button class="btn btn-delete" onclick="deleteVM(\'' + vm.id + '\', \'' + escapeHtml(vm.name) + '\')">Delete</button>';
                }

                html += '</div></td></tr>';
            });

            html += '</tbody></table>';
            container.innerHTML = html;
        }

        async function startVM(id) {
            try {
                setLoading(id, true);
                await fetchJSON(API + '/vms/' + id + '/start', { method: 'POST' });
                await loadVMs();
            } catch (e) {
                showError('Failed to start VM: ' + e.message);
                setLoading(id, false);
            }
        }

        async function stopVM(id) {
            try {
                setLoading(id, true);
                await fetchJSON(API + '/vms/' + id + '/stop', { method: 'POST' });
                await loadVMs();
            } catch (e) {
                showError('Failed to stop VM: ' + e.message);
                setLoading(id, false);
            }
        }

        async function deleteVM(id, name) {
            if (!confirm('Delete VM "' + name + '"? This cannot be undone.')) return;
            try {
                setLoading(id, true);
                await fetchJSON(API + '/vms/' + id, { method: 'DELETE' });
                await loadVMs();
            } catch (e) {
                showError('Failed to delete VM: ' + e.message);
                setLoading(id, false);
            }
        }

        function setLoading(id, loading) {
            const row = document.querySelector('tr[data-id="' + id + '"]');
            if (row) {
                row.classList.toggle('loading', loading);
                row.querySelectorAll('button').forEach(btn => btn.disabled = loading);
            }
        }

        async function copySSH(id, btn) {
            const cmd = 'ssh -t -p 2222 ' + window.location.hostname + ' connect ' + id;
            try {
                await navigator.clipboard.writeText(cmd);
                const original = btn.textContent;
                btn.textContent = 'Copied!';
                setTimeout(() => btn.textContent = original, 1500);
            } catch (e) {
                showError('Failed to copy to clipboard');
            }
        }

        document.getElementById('create-form').addEventListener('submit', async (e) => {
            e.preventDefault();
            const form = e.target;
            const btn = form.querySelector('button[type="submit"]');
            btn.disabled = true;

            const data = {
                name: form.name.value,
                os_image_id: form.os_image_id.value,
                ram_mb: parseInt(form.ram_mb.value) || 1024,
                disk_size_gb: parseInt(form.disk_size_gb.value) || 8,
                vcpu_count: parseInt(form.vcpu_count.value) || 1,
                internet_enabled: form.internet_enabled.checked
            };

            try {
                await fetchJSON(API + '/vms', {
                    method: 'POST',
                    body: JSON.stringify(data)
                });
                form.reset();
                form.ram_mb.value = '512';
                form.disk_size_gb.value = '8';
                form.vcpu_count.value = '1';
                form.internet_enabled.checked = true;
                await loadVMs();
            } catch (e) {
                showError('Failed to create VM: ' + e.message);
            } finally {
                btn.disabled = false;
            }
        });

        // Initial load
        loadOSImages();
        loadVMs();

        // Auto-refresh every 5 seconds
        setInterval(loadVMs, 5000);
    </script>
</body>
</html>`

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}
