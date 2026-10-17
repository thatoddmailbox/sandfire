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
        .container { max-width: 1100px; margin: 0 auto; }
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
        .btn-console { background: #8b5cf6; color: #fff; }
        .btn-more { background: #6b7280; color: #fff; padding: 6px 10px; }
        .actions { display: flex; gap: 8px; align-items: center; }
        .dropdown { position: relative; display: inline-block; }
        .dropdown-content {
            display: none;
            position: absolute;
            right: 0;
            top: 100%;
            background: #fff;
            min-width: 140px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.15);
            border-radius: 4px;
            z-index: 100;
            overflow: hidden;
        }
        .dropdown-content.show { display: block; }
        .dropdown-item {
            display: block;
            width: 100%;
            padding: 10px 14px;
            border: none;
            background: none;
            text-align: left;
            cursor: pointer;
            font-size: 0.85em;
            color: #333;
        }
        .dropdown-item:hover { background: #f5f5f5; }
        .dropdown-item.danger { color: #dc2626; }
        .dropdown-item.danger:hover { background: #fee2e2; }
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
        .modal-overlay {
            display: none;
            position: fixed;
            top: 0;
            left: 0;
            right: 0;
            bottom: 0;
            background: rgba(0,0,0,0.5);
            z-index: 200;
            align-items: center;
            justify-content: center;
        }
        .modal-overlay.show { display: flex; }
        .modal {
            background: #fff;
            border-radius: 8px;
            padding: 20px;
            min-width: 400px;
            max-width: 90%;
            box-shadow: 0 4px 20px rgba(0,0,0,0.3);
        }
        .modal-fixed { width: 400px; }
        .modal h3 {
            color: #d4470a;
            margin-bottom: 15px;
        }
        .modal-actions {
            display: flex;
            gap: 10px;
            justify-content: flex-end;
            margin-top: 20px;
        }
        .btn-cancel { background: #6b7280; color: #fff; }
        .btn-save { background: #d4470a; color: #fff; }
        .form-group input[readonly] { background: #f5f5f5; color: #666; }
        .disk-stats {
            display: grid;
            grid-template-columns: auto 1fr;
            gap: 6px 16px;
            margin-bottom: 15px;
            font-size: 0.9em;
        }
        .disk-stats dt { color: #666; }
        .disk-note {
            margin-top: 12px;
            padding: 10px 12px;
            border-radius: 4px;
            font-size: 0.85em;
        }
        .disk-note-warn { background: #fef3c7; color: #92400e; }
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
                        <input type="text" id="disk_size_gb" placeholder="From OS image" readonly title="Disks start at the OS image's size. Use Resize Disk afterwards to change it.">
                    </div>
                    <div class="form-group">
                        <label for="vcpu_count">vCPUs</label>
                        <input type="number" id="vcpu_count" name="vcpu_count" value="1" min="1" max="8">
                    </div>
                </div>
                <div class="form-actions">
                    <button type="submit" class="btn btn-create">Create VM</button>
                </div>
            </form>
        </div>
    </div>

    <div id="edit-modal" class="modal-overlay">
        <div class="modal">
            <h3>Edit VM</h3>
            <form id="edit-form">
                <input type="hidden" id="edit-vm-id">
                <div class="form-grid">
                    <div class="form-group">
                        <label for="edit-name">Name</label>
                        <input type="text" id="edit-name" required placeholder="my-vm">
                    </div>
                    <div class="form-group">
                        <label for="edit-vcpu">vCPUs</label>
                        <input type="number" id="edit-vcpu" min="1" max="8" required>
                    </div>
                    <div class="form-group">
                        <label for="edit-ram">RAM (MB)</label>
                        <input type="number" id="edit-ram" min="128" step="128" required>
                    </div>
                </div>
                <div class="modal-actions">
                    <button type="button" class="btn btn-cancel" onclick="closeEditModal()">Cancel</button>
                    <button type="submit" class="btn btn-save">Save</button>
                </div>
            </form>
        </div>
    </div>

    <div id="resize-modal" class="modal-overlay">
        <div class="modal modal-fixed">
            <h3>Resize Disk</h3>
            <form id="resize-form">
                <input type="hidden" id="resize-vm-id">
                <p id="resize-loading" style="color:#666">Inspecting disk...</p>
                <div id="resize-body" style="display:none">
                    <dl class="disk-stats">
                        <dt>VM</dt><dd id="resize-vm-name"></dd>
                        <dt>Current size</dt><dd id="resize-current"></dd>
                        <dt>Used</dt><dd id="resize-used"></dd>
                        <dt>Minimum size</dt><dd id="resize-min"></dd>
                    </dl>
                    <div class="form-group">
                        <label for="resize-size">New size (GB)</label>
                        <input type="number" id="resize-size" min="1" step="1" required>
                    </div>
                    <div id="resize-note" class="disk-note" style="display:none"></div>
                </div>
                <div class="modal-actions">
                    <button type="button" class="btn btn-cancel" onclick="closeResizeModal()">Cancel</button>
                    <button type="submit" class="btn btn-save" id="resize-submit" disabled>Resize</button>
                </div>
            </form>
        </div>
    </div>

    <script>
        const API = '/api';
        const GiB = 1024 * 1024 * 1024;
        let osImages = {};

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
                osImages = {};
                images.forEach(img => {
                    osImages[img.id] = img;
                    const opt = document.createElement('option');
                    opt.value = img.id;
                    opt.textContent = img.name + ' (' + img.id + ')';
                    select.appendChild(opt);
                });
            } catch (e) {
                showError('Failed to load OS images: ' + e.message);
            }
        }

        document.getElementById('os_image_id').addEventListener('change', function() {
            const img = osImages[this.value];
            if (img) {
                document.getElementById('disk_size_gb').value = img.rootfs_size_gb + ' GB';
                document.getElementById('ram_mb').value = img.suggested_ram_mb;
            }
        });

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
                const specs = vm.vcpu_count + (vm.vcpu_count === 1 ? ' vCPU, ' : ' vCPUs, ') + vm.ram_mb + ' MB RAM, ' + vm.disk_size_gb + ' GB';
                const osImage = osImages[vm.os_image_id]?.name || vm.os_image_id;

                html += '<tr data-id="' + escapeHtml(vm.id) + '">';
                html += '<td><strong>' + escapeHtml(vm.name) + '</strong><br><small style="color:#999"><a href="https://' + escapeHtml(vm.id) + '.' + window.location.hostname + '" target="_blank" style="color:#999;text-decoration:none">' + escapeHtml(vm.id) + '</a></small></td>';
                html += '<td><span class="state ' + stateClass + '">' + escapeHtml(vm.state) + '</span></td>';
                html += '<td>' + ip + '</td>';
                html += '<td><span class="vm-specs">' + escapeHtml(osImage) + '</span></td>';
                html += '<td><span class="vm-specs">' + escapeHtml(specs) + '</span></td>';
                html += '<td><div class="actions">';
                html += '<button class="btn btn-copy" onclick="copySSH(\'' + vm.id + '\', this)">Copy SSH</button>';

                if (vm.state === 'running') {
                    html += '<button class="btn btn-console" onclick="openConsole(\'' + vm.id + '\')">Console</button>';
                    html += '<button class="btn btn-stop" onclick="stopVM(\'' + vm.id + '\')">Stop</button>';
                } else if (vm.state === 'stopped' || vm.state === 'error') {
                    html += '<button class="btn btn-start" onclick="startVM(\'' + vm.id + '\')">Start</button>';
                    html += '<div class="dropdown">';
                    html += '<button class="btn btn-more" onclick="toggleDropdown(event, \'' + vm.id + '\')">More</button>';
                    html += '<div id="dropdown-' + vm.id + '" class="dropdown-content">';
                    html += '<button class="dropdown-item" onclick="openEditModal(\'' + vm.id + '\', \'' + escapeHtml(vm.name) + '\', ' + vm.vcpu_count + ', ' + vm.ram_mb + ')">Edit</button>';
                    html += '<button class="dropdown-item" onclick="openResizeModal(\'' + vm.id + '\', \'' + escapeHtml(vm.name) + '\')">Resize Disk</button>';
                    html += '<button class="dropdown-item danger" onclick="resetVMDisk(\'' + vm.id + '\', \'' + escapeHtml(vm.name) + '\')">Reset Disk</button>';
                    html += '<button class="dropdown-item danger" onclick="deleteVM(\'' + vm.id + '\', \'' + escapeHtml(vm.name) + '\')">Delete</button>';
                    html += '</div></div>';
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
            closeAllDropdowns();
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

        async function resetVMDisk(id, name) {
            closeAllDropdowns();
            if (!confirm('Reset disk for VM "' + name + '"? This will replace the disk with a fresh copy of the OS image. All data on the disk will be lost. This cannot be undone.')) return;
            try {
                setLoading(id, true);
                await fetchJSON(API + '/vms/' + id + '/reset-disk', { method: 'POST' });
                await loadVMs();
            } catch (e) {
                showError('Failed to reset VM disk: ' + e.message);
                setLoading(id, false);
            }
        }

        function openEditModal(id, name, vcpu, ram) {
            closeAllDropdowns();
            document.getElementById('edit-vm-id').value = id;
            document.getElementById('edit-name').value = name;
            document.getElementById('edit-vcpu').value = vcpu;
            document.getElementById('edit-ram').value = ram;
            document.getElementById('edit-modal').classList.add('show');
        }

        function closeEditModal() {
            document.getElementById('edit-modal').classList.remove('show');
        }

        document.getElementById('edit-form').addEventListener('submit', async (e) => {
            e.preventDefault();
            const id = document.getElementById('edit-vm-id').value;
            const name = document.getElementById('edit-name').value;
            const vcpu = parseInt(document.getElementById('edit-vcpu').value);
            const ram = parseInt(document.getElementById('edit-ram').value);
            const btn = e.target.querySelector('button[type="submit"]');
            btn.disabled = true;

            try {
                await fetchJSON(API + '/vms/' + id, {
                    method: 'PUT',
                    body: JSON.stringify({
                        name: name,
                        vcpu_count: vcpu,
                        ram_mb: ram,
                        internet_enabled: true
                    })
                });
                closeEditModal();
                await loadVMs();
            } catch (err) {
                showError('Failed to update VM: ' + err.message);
            } finally {
                btn.disabled = false;
            }
        });

        // Close edit modal when clicking overlay
        document.getElementById('edit-modal').addEventListener('click', (e) => {
            if (e.target.id === 'edit-modal') closeEditModal();
        });

        let resizeDisk = null;

        function formatGB(bytes) {
            return (bytes / GiB).toFixed(1) + ' GB';
        }

        async function openResizeModal(id, name) {
            closeAllDropdowns();
            resizeDisk = null;
            document.getElementById('resize-vm-id').value = id;
            document.getElementById('resize-vm-name').textContent = name;
            document.getElementById('resize-loading').textContent = 'Inspecting disk...';
            document.getElementById('resize-loading').style.display = '';
            document.getElementById('resize-body').style.display = 'none';
            document.getElementById('resize-submit').disabled = true;
            document.getElementById('resize-modal').classList.add('show');

            try {
                const info = await fetchJSON(API + '/vms/' + id + '/disk');
                if (document.getElementById('resize-vm-id').value !== id) return;
                resizeDisk = info;
                const minGB = Math.max(1, Math.ceil(info.min_size_bytes / GiB));
                document.getElementById('resize-current').textContent = formatGB(info.size_bytes);
                document.getElementById('resize-used').textContent = formatGB(info.used_bytes) + ' (' + Math.round(100 * info.used_bytes / info.size_bytes) + '%)';
                document.getElementById('resize-min').textContent = minGB + ' GB';
                const input = document.getElementById('resize-size');
                input.min = minGB;
                input.value = Math.ceil(info.size_bytes / GiB);
                document.getElementById('resize-loading').style.display = 'none';
                document.getElementById('resize-body').style.display = '';
                updateResizeNote();
                input.focus();
            } catch (e) {
                document.getElementById('resize-loading').textContent = 'Failed to inspect disk: ' + e.message;
            }
        }

        function closeResizeModal() {
            document.getElementById('resize-modal').classList.remove('show');
            resizeDisk = null;
        }

        function updateResizeNote() {
            const note = document.getElementById('resize-note');
            const submit = document.getElementById('resize-submit');
            const input = document.getElementById('resize-size');
            const newGB = parseInt(input.value);
            const newBytes = newGB * GiB;
            submit.disabled = true;
            note.style.display = 'none';
            if (!resizeDisk || !(newBytes > 0)) return;

            note.style.display = '';
            if (newBytes < resizeDisk.min_size_bytes) {
                note.className = 'disk-note disk-note-warn';
                note.textContent = 'Too small: the filesystem needs at least ' + input.min + ' GB.';
            } else if (newGB === Math.ceil(resizeDisk.size_bytes / GiB)) {
                // The input starts at the current size rounded up to whole GB,
                // so treat that as unchanged even if the disk is slightly smaller.
                note.style.display = 'none';
            } else if (newBytes > resizeDisk.size_bytes) {
                note.style.display = 'none';
                submit.disabled = false;
            } else {
                note.className = 'disk-note disk-note-warn';
                note.textContent = 'Shrinking moves data around inside the filesystem and may take a while.';
                submit.disabled = false;
            }
        }

        document.getElementById('resize-size').addEventListener('input', updateResizeNote);

        document.getElementById('resize-form').addEventListener('submit', async (e) => {
            e.preventDefault();
            const id = document.getElementById('resize-vm-id').value;
            const sizeGB = parseInt(document.getElementById('resize-size').value);
            const btn = document.getElementById('resize-submit');
            btn.disabled = true;
            btn.textContent = 'Resizing...';
            setLoading(id, true);

            try {
                await fetchJSON(API + '/vms/' + id + '/resize-disk', {
                    method: 'POST',
                    body: JSON.stringify({ size_gb: sizeGB })
                });
                closeResizeModal();
            } catch (err) {
                showError('Failed to resize VM disk: ' + err.message);
                btn.disabled = false;
            } finally {
                btn.textContent = 'Resize';
                await loadVMs();
            }
        });

        document.getElementById('resize-modal').addEventListener('click', (e) => {
            if (e.target.id === 'resize-modal' && document.getElementById('resize-submit').textContent !== 'Resizing...') closeResizeModal();
        });

        function toggleDropdown(event, vmId) {
            event.stopPropagation();
            const dropdown = document.getElementById('dropdown-' + vmId);
            const wasOpen = dropdown.classList.contains('show');
            closeAllDropdowns();
            if (!wasOpen) {
                dropdown.classList.add('show');
            }
        }

        function closeAllDropdowns() {
            document.querySelectorAll('.dropdown-content').forEach(d => d.classList.remove('show'));
        }

        function isAnyDropdownOpen() {
            return document.querySelector('.dropdown-content.show') !== null;
        }

        // Close dropdowns when clicking outside
        document.addEventListener('click', closeAllDropdowns);

        function setLoading(id, loading) {
            const row = document.querySelector('tr[data-id="' + id + '"]');
            if (row) {
                row.classList.toggle('loading', loading);
                row.querySelectorAll('button').forEach(btn => btn.disabled = loading);
            }
        }

        function openConsole(id) {
            window.open('/console/' + id, '_blank', 'width=1024,height=768');
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
                vcpu_count: parseInt(form.vcpu_count.value) || 1,
                internet_enabled: true
            };

            try {
                await fetchJSON(API + '/vms', {
                    method: 'POST',
                    body: JSON.stringify(data)
                });
                form.reset();
                form.ram_mb.value = '512';
                form.vcpu_count.value = '1';
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

        // Auto-refresh every 5 seconds (skip if dropdown is open)
        setInterval(() => {
            if (!isAnyDropdownOpen()) {
                loadVMs();
            }
        }, 5000);
    </script>
</body>
</html>`

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}
