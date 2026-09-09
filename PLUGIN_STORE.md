# 通过 CPA 插件商店安装

本仓库提供一个可直接加入 CPA 的自定义 Plugin Store registry：

```text
https://raw.githubusercontent.com/kitdine/cpa-plugin-key-chain-router/main/registry.json
```

在 CPA `config.yaml` 中加入：

```yaml
plugins:
  enabled: true
  dir: plugins
  store-sources:
    - "https://raw.githubusercontent.com/kitdine/cpa-plugin-key-chain-router/main/registry.json"
  configs:
    key-chain-router:
      enabled: true
      priority: 100
```

重启 CPA 后，打开管理面板的 **插件商店 / Plugin Store**，即可看到 `Key Chain Router`，点击安装。

本插件使用 CPA `github-release` 安装约定。Release 会包含：

```text
key-chain-router_<version>_linux_amd64.zip
checksums.txt
```

例如 v0.3.0：

```text
key-chain-router_0.3.0_linux_amd64.zip
checksums.txt
```

其中 zip 根目录只包含目标动态库：

```text
key-chain-router-v0.3.0.so
```

CPA 会先使用 `checksums.txt` 校验 zip，然后安装到：

```text
plugins/linux/amd64/key-chain-router-v0.3.0.so
```

安装完成后，如插件尚未生效，重启 CPA 容器：

```bash
docker compose restart cli-proxy-api
```

随后访问：

```text
http://<CPA_HOST>:8317/v0/resource/plugins/key-chain-router/status
```
