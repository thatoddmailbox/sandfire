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
  "vcpu_count": 2,
  "internet_enabled": true
}
```

The VM's disk starts at the size of the OS image's rootfs. Use [Resize Disk](#resize-disk-must-be-stopped) to change it.

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

## Get Disk Info (must be stopped)

```
GET /api/vms/{id}/disk
```

Response (all values in bytes):
```json
{
  "size_bytes": 8589934592,
  "used_bytes": 3221225472,
  "min_size_bytes": 3758096384
}
```

`used_bytes` includes filesystem metadata. `min_size_bytes` is the smallest size the disk can be shrunk to.

## Resize Disk (must be stopped)

```
POST /api/vms/{id}/resize-disk
Content-Type: application/json

{
  "size_gb": 32
}
```

Grows or shrinks the VM's disk and its filesystem to `size_gb` GiB. Shrinking fails without changing anything if the filesystem doesn't fit in the new size.

## Reset Disk (must be stopped)

```
POST /api/vms/{id}/reset-disk
```

Replaces the VM's disk with a fresh copy of its OS image. If the disk was larger than the image, it keeps its current size.

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
