# syntax=docker/dockerfile:1

FROM gcr.io/distroless/static-debian12:nonroot

ARG TARGETPLATFORM

COPY ${TARGETPLATFORM}/codex-gateway /codex-gateway

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/codex-gateway"]
CMD ["serve"]