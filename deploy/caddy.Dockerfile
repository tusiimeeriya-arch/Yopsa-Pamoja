# Caddy image with writable /data and /config owned by the unprivileged user.
FROM caddy:2.9.1-alpine
RUN chown -R 1000:1000 /data /config
USER 1000:1000
