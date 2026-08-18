FROM scratch

WORKDIR /app

COPY api/bin/flow-api /app/flow-api
COPY web/dist /app/web

ENV FLOW_DB_PATH=/app/data/flow.db
ENV FLOW_UPLOAD_PATH=/app/data/uploads
ENV FLOW_STATIC_PATH=/app/web
ENV FLOW_APP_URL=http://127.0.0.1:5173

EXPOSE 8080

CMD ["/app/flow-api"]
