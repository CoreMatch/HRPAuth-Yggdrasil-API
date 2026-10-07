-- 1. Add cbh, last_sign_at, password to accounts table
ALTER TABLE `accounts` ADD COLUMN `cbh` tinyint(1) NOT NULL DEFAULT 1 COMMENT '1 = Human, 0 = Proxy/Bot' AFTER `mbe`;
ALTER TABLE `accounts` ADD COLUMN `password` varchar(255) DEFAULT NULL AFTER `mojang_uuid`;
ALTER TABLE `accounts` ADD COLUMN `last_sign_at` timestamp NULL DEFAULT NULL AFTER `cbh`;

-- 2. Modify core_user_id to be nullable
ALTER TABLE `accounts` MODIFY COLUMN `core_user_id` varchar(32) COLLATE utf8mb4_unicode_ci DEFAULT NULL COMMENT 'Foreign key to Core users.uuid';
