CREATE TABLE IF NOT EXISTS `texture_list_skin` (
    `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
    `hash` varchar(64) NOT NULL,
    `account_id` int(11) NOT NULL,
    `model` enum('default','slim') NOT NULL DEFAULT 'default',
    `width` int(11) NOT NULL DEFAULT '0',
    `height` int(11) NOT NULL DEFAULT '0',
    `file_name` varchar(255) DEFAULT NULL,
    `previewfile` varchar(255) DEFAULT NULL,
    `name` varchar(20) DEFAULT NULL,
    `description` text,
    `tags` varchar(255) DEFAULT NULL,
    `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_texture_list_account_hash` (`account_id`,`hash`),
    KEY `idx_texture_list_account` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `texture_list_cape` (
    `id` int(11) unsigned NOT NULL AUTO_INCREMENT,
    `hash` varchar(64) NOT NULL,
    `account_id` int(11) NOT NULL,
    `width` int(11) NOT NULL DEFAULT '0',
    `height` int(11) NOT NULL DEFAULT '0',
    `file_name` varchar(255) DEFAULT NULL,
    `previewfile` varchar(255) DEFAULT NULL,
    `name` varchar(20) DEFAULT NULL,
    `description` text,
    `tags` varchar(255) DEFAULT NULL,
    `created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
    `updated_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (`id`),
    UNIQUE KEY `uk_texture_list_account_hash` (`account_id`,`hash`),
    KEY `idx_texture_list_account` (`account_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
