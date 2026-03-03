# API Reference

## Health Check

```
GET /health
```

Response:
```json
{"status": "ok"}
```

## List OS Images

```
GET /api/os-images
```

## List VMs

```
GET /api/vms
```

## Create VM

```
POST /api/vms
Content-Type: application/json

{
  "name": "my-vm",
  "os_image_id": "ubuntu-24.04",
  "ram_mb": 1024,
  "disk_size_gb": 10,
  "vcpu_count": 2,
  "internet_enabled": true
}
```

## Get VM

```
GET /api/vms/{id}
```

## Update VM (must be stopped)

```
PUT /api/vms/{id}
Content-Type: application/json

{
  "ram_mb": 2048,
  "vcpu_count": 4
}
```

## Delete VM (must be stopped)

```
DELETE /api/vms/{id}
```

## Start VM

```
POST /api/vms/{id}/start
```

## Stop VM

```
POST /api/vms/{id}/stop
```
