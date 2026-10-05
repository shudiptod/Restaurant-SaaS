DROP POLICY IF EXISTS platform_admin_all ON account_feature_overrides;
DROP POLICY IF EXISTS platform_admin_read ON account_feature_overrides;
DROP POLICY IF EXISTS platform_admin_read ON account_subscriptions;
DROP INDEX IF EXISTS platform_admins_user_unique;