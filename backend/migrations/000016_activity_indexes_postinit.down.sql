-- 000016 only defensively creates missing indexes that are also present
-- in earlier table definitions. Do not drop them here, otherwise rolling
-- back 000016 can remove indexes owned by 000013 / 000015.
SELECT 1;
