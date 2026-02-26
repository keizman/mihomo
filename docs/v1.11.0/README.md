# v1.11.0 Test Artifacts

本目录保留 Android 复测可复用资产，包含脚本与原始测试结果。

## 脚本

- `scripts/netsim_test_server.py`
  - Windows 本地 HTTP 测试服务 (`/ping`, `/blob`, `/upload`)
- `scripts/netsim_download_fix_verify.py`
  - 下载限速修复后的定向验证脚本（仅前后对照）

## 数据

- `data/netsim_download_fix_verify.json`
  - 下载限速修复前后对照原始结果

## 说明

- 详细结论见: `v1.11.0.md`
