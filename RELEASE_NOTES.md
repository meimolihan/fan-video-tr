日常更新

## Docker 安装
```bash
docker pull mobufan/fan-video-tr:latest
docker run -d --name fan-video-tr -p 8790:8790 -v /var/lib/fan-video-tr:/data mobufan/fan-video-tr:v0.0.2
```

```bash
docker pull ghcr.io/meimolihan/fan-video-tr:v0.0.2
```

## 二进制安装
### Linux amd64 / arm64
```bash
bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-video-tr/main/scripts/install.sh)" -p 8790
```

## 二进制卸载
```bash
bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-video-tr/main/scripts/uninstall.sh)" -y
```
