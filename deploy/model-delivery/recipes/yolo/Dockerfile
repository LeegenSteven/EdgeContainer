ARG BASE_IMAGE
FROM ${BASE_IMAGE}
USER 0:0
RUN mkdir -p /opt/edgestack/models && chmod 0555 /opt/edgestack/models
# The approved base contains RKNN dependencies, frozen workload and runtime config.
COPY --chmod=0444 payload/model.rknn /opt/edgestack/models/model.rknn
COPY --chmod=0555 delivery_runtime/ /opt/edgestack/delivery_runtime/
LABEL io.edgestack.delivery.recipe="prepared-yolo-rknn-v1"
USER 1000:1000
ENTRYPOINT ["python3", "/opt/edgestack/delivery_runtime/runtime_service.py", "--config", "/opt/edgestack/runtime-config.json"]
HEALTHCHECK --interval=10s --timeout=5s --start-period=300s --retries=3 CMD ["python3", "/opt/edgestack/delivery_runtime/runtime_health.py"]
