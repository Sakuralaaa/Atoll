-- Running commands inside containers moved out of "deploy" into its own "exec" permission.
-- Tokens that could deploy before keep exec, so nothing that works today breaks; new tokens
-- get exec only when it is asked for.
UPDATE api_tokens SET permissions = permissions || ',exec'
 WHERE (',' || permissions || ',') LIKE '%,deploy,%'
   AND (',' || permissions || ',') NOT LIKE '%,exec,%';
