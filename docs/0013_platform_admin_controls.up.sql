DELETE FROM platform_admins existing
USING (
  SELECT id, row_number() OVER (
    PARTITION BY user_id
    ORDER BY (role = 'superadmin') DESC, created_at, id
  ) AS position
  FROM platform_admins
) ranked
WHERE existing.id = ranked.id AND ranked.position > 1;

CREATE UNIQUE INDEX platform_admins_user_unique ON platform_admins (user_id);

CREATE POLICY platform_admin_all ON account_feature_overrides
  FOR ALL
  USING (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role = 'superadmin'
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role = 'superadmin'
  ));

CREATE POLICY platform_admin_read ON account_feature_overrides
  FOR SELECT
  USING (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role IN ('superadmin', 'support')
  ));

CREATE POLICY platform_admin_read ON account_subscriptions
  FOR SELECT
  USING (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role IN ('superadmin', 'support')
  ));