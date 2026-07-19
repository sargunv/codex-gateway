FROM gcr.io/distroless/static-debian12@sha256:597c2b4bc7f353100af9b8b06bb4f126c4a45f9d8175e25d4f01f965d5d94396

ARG TARGETPLATFORM

COPY ${TARGETPLATFORM}/agent-api-gateway /agent-api-gateway

# The process constructs provider activation from explicit environment variables
# and mounted native credential directories. Listen on 0.0.0.0 so port-forwarded
# traffic reaches us; the -p mapping controls host-side exposure.
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/agent-api-gateway"]
CMD ["serve", "--addr", "0.0.0.0:8080"]
