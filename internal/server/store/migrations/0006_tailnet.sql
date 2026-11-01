-- Private network (Tailscale) and cross-server service placement.
ALTER TABLE nodes ADD COLUMN tailnet_ip TEXT NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN network TEXT NOT NULL DEFAULT '';      -- JSON api.NodeNetwork reported by the agent
ALTER TABLE services ADD COLUMN server_id TEXT NOT NULL DEFAULT ''; -- '' = the project's server
-- Ports published on a node's tailnet IP for services consumed from other servers.
CREATE TABLE mesh_ports (
  service_id TEXT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
  port       INTEGER NOT NULL,
  protocol   TEXT NOT NULL,
  node_id    TEXT NOT NULL,
  host_port  INTEGER NOT NULL,
  PRIMARY KEY (service_id, port, protocol),
  UNIQUE (node_id, host_port, protocol)
);
