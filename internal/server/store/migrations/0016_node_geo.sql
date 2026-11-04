-- GeoIP record of the node's public address (JSON api.NodeGeo; '' = not looked up).
ALTER TABLE nodes ADD COLUMN geo TEXT NOT NULL DEFAULT '';
