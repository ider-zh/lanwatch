# 说明：本机无 Docker Hub 访问，golang/node 基础镜像拉不下来。
# 因此 Go 二进制与前端 dist 在宿主机预构建（见下方“构建流程”），
# 这里只用一个已缓存的 alpine:latest 把二进制打包成镜像。
# 若日后有 registry 访问，可改回多阶段 Dockerfile。

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY build/lanwatch /app/lanwatch
EXPOSE 9123
ENTRYPOINT ["/app/lanwatch"]
CMD ["-config", "/etc/lanwatch/config.yaml"]
