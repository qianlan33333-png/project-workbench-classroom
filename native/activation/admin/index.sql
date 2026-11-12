SELECT 'shell' AS component, '激活码管理' AS title, 'zh-CN' AS language,
       'light' AS theme, FALSE AS fixed_top_menu;

SET issued_code = sqlpage.header('X-Workbench-Issued-Code');
SELECT 'code' AS component WHERE $issued_code IS NOT NULL;
SELECT '激活码已生成' AS title, $issued_code AS contents,
       '复制此码发给使用者。领取期限为 7 天。' AS description
WHERE $issued_code IS NOT NULL;

SELECT 'datagrid' AS component;
WITH totals AS (
    SELECT COUNT(*) AS issued,
           COUNT(claimed_at) AS redeemed,
           COUNT(CASE WHEN expires_at >=
               DATE(UTC_TIMESTAMP() + INTERVAL 8 HOUR) + INTERVAL 7 DAY - INTERVAL 8 HOUR
               THEN 1 END) AS today
    FROM wb_activation_codes
)
SELECT '今日新增' AS title, CAST(today AS CHAR) AS description, TRUE AS active FROM totals
UNION ALL
SELECT '累计生成', CAST(issued AS CHAR), FALSE FROM totals
UNION ALL
SELECT '已兑换', CAST(redeemed AS CHAR), FALSE FROM totals;

SELECT 'form' AS component, '生成激活码' AS validate, '/activation-codes/issue' AS action;
SELECT 'request' AS name, 'hidden' AS type, LOWER(HEX(RANDOM_BYTES(16))) AS value;

SELECT 'form' AS component, '查询' AS validate, TRUE AS validate_outline,
       '/activation-codes/' AS action;
SELECT 'code' AS name, '粘贴原码查兑换状态' AS label, 'text' AS type,
       32 AS maxlength, TRUE AS required, 'off' AS autocomplete;

SET lookup_code = CASE WHEN sqlpage.request_method() = 'POST' THEN NULLIF(TRIM(:code), '') END;
SELECT 'alert' AS component,
       CASE
           WHEN NOT REGEXP_LIKE($lookup_code, '^[A-Za-z0-9_-]{32}$', 'c') THEN '请输入完整的激活码'
           WHEN codes.digest IS NULL THEN '未找到该激活码'
           WHEN codes.claimed_at IS NOT NULL THEN CONCAT('已兑换 · ', codes.claimed_at + INTERVAL 8 HOUR)
           WHEN codes.expires_at <= UTC_TIMESTAMP() THEN '已过期'
           ELSE '待兑换'
       END AS title,
       CASE WHEN codes.digest IS NOT NULL
           THEN CONCAT('记录编号 ', LEFT(HEX(codes.digest), 12))
       END AS description,
       'info' AS color
FROM (SELECT $lookup_code AS code) AS lookup
LEFT JOIN wb_activation_codes AS codes ON codes.digest = UNHEX(SHA2(lookup.code, 256))
WHERE lookup.code IS NOT NULL;

SELECT 'title' AS component, '发码记录' AS contents, 2 AS level;
SELECT 'text' AS component, '暂无发码记录。' AS contents
WHERE NOT EXISTS (SELECT 1 FROM wb_activation_codes);
SELECT 'table' AS component, TRUE AS sort, TRUE AS search
WHERE EXISTS (SELECT 1 FROM wb_activation_codes);
SELECT LEFT(HEX(digest), 12) AS '记录编号',
       CASE
           WHEN claimed_at IS NOT NULL THEN '已兑换'
           WHEN expires_at <= UTC_TIMESTAMP() THEN '已过期'
           ELSE '待兑换'
       END AS '状态',
       CAST(expires_at - INTERVAL 7 DAY + INTERVAL 8 HOUR AS CHAR) AS '生成时间',
       CAST(expires_at + INTERVAL 8 HOUR AS CHAR) AS '领取截止',
       COALESCE(CAST(claimed_at + INTERVAL 8 HOUR AS CHAR), '—') AS '兑换时间'
FROM wb_activation_codes
ORDER BY expires_at DESC, digest;
