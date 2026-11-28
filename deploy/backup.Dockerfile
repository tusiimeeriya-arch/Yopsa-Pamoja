# Backup image: PostgreSQL 16 client matching the server, age for encryption,
# AWS CLI for the S3-compatible Vultr Object Storage endpoint.
FROM postgres:16.9-alpine
RUN apk add --no-cache age aws-cli curl tzdata bash \
    && mkdir -p /var/log/pamoja /scripts \
    && chown -R postgres:postgres /var/log/pamoja /scripts
USER postgres
WORKDIR /scripts
CMD ["bash", "/scripts/backup.sh"]
