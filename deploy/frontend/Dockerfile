# 前端镜像 = nginx 入口网关：构建 Vue dist 并作为静态站点 + TLS/反代/SSE 透传
# 对应 CLAUDE.md《部署架构》：前端构建产物打进 nginx 镜像，前端改动只重建此镜像
FROM node:22-alpine AS builder
WORKDIR /app
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM nginx:alpine
COPY deploy/frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html
EXPOSE 80 443
# 静态探测：/healthz 由 nginx 自身返回（nginx.conf 内 location = /healthz）
HEALTHCHECK --interval=15s --timeout=3s --retries=5 \
  CMD wget -qO- http://localhost/healthz >/dev/null || exit 1
