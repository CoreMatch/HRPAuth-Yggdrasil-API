CREATE TABLE IF NOT EXISTS `texture_list_skin` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `hash` varchar(64) NOT NULL,
    `account_id` bigint NOT NULL,
    `model` enum('default','slim') NOT NULL DEFAULT 'default',
    `width` int NOT NULL DEFAULT 0,
    `height` int NOT NULL DEFAULT 0,
    `file_name` varchar(255) DEFAULT NULL,
    `previewfile` varchar(255) DEFAULT NULL,
    `created_at` datetime(3) DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` datetime(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_texture_list_account_hash` (`account_id`,`hash`),
    KEY `idx_texture_list_account` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS `texture_list_cape` (
    `id` bigint unsigned NOT NULL AUTO_INCREMENT,
    `hash` varchar(64) NOT NULL,
    `account_id` bigint NOT NULL,
    `width` int NOT NULL DEFAULT 0,
    `height` int NOT NULL DEFAULT 0,
    `file_name` varchar(255) DEFAULT NULL,
    `previewfile` varchar(255) DEFAULT NULL,
    `created_at` datetime(3) DEFAULT CURRENT_TIMESTAMP(3),
    `updated_at` datetime(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_texture_list_account_hash` (`account_id`,`hash`),
    KEY `idx_texture_list_account` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
