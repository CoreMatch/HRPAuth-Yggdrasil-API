ALTER TABLE `accounts`
  ADD COLUMN `skin_sync` tinyint(1) NOT NULL DEFAULT 1 COMMENT '1 = mirror the bound official skin automatically (default), 0 = never touch profile textures',
  ADD COLUMN `skin_sync_mark` varchar(128) DEFAULT NULL COMMENT 'last successful sync: <mojang_uuid>:<texture hash|default>; NULL = never synced';
