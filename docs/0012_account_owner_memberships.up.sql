CREATE POLICY account_owner_manage_memberships ON restaurant_users
  FOR ALL
  USING (EXISTS (
    SELECT 1 FROM restaurants r
    JOIN accounts a ON a.id = r.account_id
    WHERE r.id = restaurant_users.restaurant_id
      AND a.owner_user_id = current_setting('app.current_user_id', true)::uuid
  ))
  WITH CHECK (EXISTS (
    SELECT 1 FROM restaurants r
    JOIN accounts a ON a.id = r.account_id
    WHERE r.id = restaurant_users.restaurant_id
      AND a.owner_user_id = current_setting('app.current_user_id', true)::uuid
  ));