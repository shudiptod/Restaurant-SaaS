CREATE POLICY platform_admin_all ON restaurant_users
  FOR ALL
  USING (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role = 'superadmin'
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role = 'superadmin'
  ));

CREATE POLICY platform_admin_all ON account_subscriptions
  FOR ALL
  USING (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role = 'superadmin'
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM platform_admins
    WHERE user_id = current_setting('app.current_user_id', true)::uuid AND role = 'superadmin'
  ));