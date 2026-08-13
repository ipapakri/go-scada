FROM nodered/node-red:4.1.11-22

WORKDIR /data
COPY --chown=node-red:node-red package.json package-lock.json ./
RUN npm ci --omit=dev

COPY --chown=node-red:node-red settings.js flows.json ./
COPY --chown=node-red:node-red lib ./lib

EXPOSE 1880 1502 1503 1504
WORKDIR /usr/src/node-red
