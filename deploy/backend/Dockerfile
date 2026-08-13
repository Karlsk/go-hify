# 后端镜像：多阶段构建，Go 1.26 编译 → alpine 运行（非 root）
# 对应 CLAUDE.md《部署架构》：hify 单二进制 + alpine，非 root，-ldflags="-s -w"
FROM golang:1.26-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/hify ./cmd/hify

FROM alpine:3.20
# 非 root 运行；logs 目录属主给 app，命名卷首次挂载会继承此权限
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=builder /out/hify /app/hify
RUN mkdir -p /app/logs && chown -R app:app /app
USER app
EXPOSE 8080
# /health 探活（busybox wget）
HEALTHCHECK --interval=10s --timeout=3s --retries=5 \
  CMD wget -qO- http://localhost:8080/health >/dev/null || exit 1
ENTRYPOINT ["/app/hify"]
