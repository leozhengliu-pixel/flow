FROM alpine:3.22 AS certificates
RUN apk add --no-cache ca-certificates

FROM scratch

WORKDIR /app

COPY api/bin/flow-api /app/flow-api
COPY web/dist /app/web
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV FLOW_DATABASE_DRIVER=sqlite
ENV FLOW_DATABASE_PATH=/app/data/flow.db
ENV FLOW_STORAGE_DRIVER=local
ENV FLOW_STORAGE_LOCAL_PATH=/app/data/uploads
ENV FLOW_STATIC_PATH=/app/web
ENV FLOW_APP_URL=http://127.0.0.1:5173

EXPOSE 8080

CMD ["/app/flow-api"]
