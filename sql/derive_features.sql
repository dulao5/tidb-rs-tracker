-- First-cut feature derivation from per-test coverage.
--
-- A function's specificity is its inverse document frequency over mapped
-- tests: idf = ln(N / df). Shared plumbing (session, bootstrap, planner
-- entry points) is reached by nearly every SQL-level test and gets idf ~ 0,
-- so it no longer dominates a test's signature.
--
-- Features v0: one feature per Go source file (module = its package dir).
-- test_feature.weight = share of the test's total idf mass in that file.
-- Refine later (e.g. cluster files by co-coverage) without changing callers.

DROP VIEW IF EXISTS func_idf;
CREATE VIEW func_idf AS
SELECT f.id AS func_id,
       count(tf.test_id) AS df,
       ln((SELECT count(DISTINCT test_id) FROM test_func) * 1.0 / count(tf.test_id)) AS idf
FROM go_func f JOIN test_func tf ON tf.func_id = f.id
GROUP BY f.id;

DELETE FROM test_feature;
DELETE FROM feature WHERE go_symbols IS NULL OR status = 'todo';

INSERT OR IGNORE INTO module(name, go_pkg)
SELECT DISTINCT rtrim(file, replace(file, '/', '')), rtrim(file, replace(file, '/', ''))
FROM go_func;
UPDATE module SET name = rtrim(name, '/'), go_pkg = rtrim(go_pkg, '/');

INSERT OR IGNORE INTO feature(module_id, name, go_symbols)
SELECT m.id, f.file, json_group_array(f.name)
FROM go_func f JOIN module m ON m.go_pkg = rtrim(rtrim(f.file, replace(f.file, '/', '')), '/')
GROUP BY f.file;

INSERT INTO test_feature(test_id, feature_id, weight)
SELECT x.test_id, fe.id, x.mass / t.total
FROM (
  SELECT tf.test_id, f.file, sum(i.idf) AS mass
  FROM test_func tf JOIN go_func f ON f.id = tf.func_id JOIN func_idf i ON i.func_id = f.id
  GROUP BY tf.test_id, f.file
) x
JOIN (
  SELECT tf.test_id, sum(i.idf) AS total
  FROM test_func tf JOIN func_idf i ON i.func_id = tf.func_id
  GROUP BY tf.test_id
) t ON t.test_id = x.test_id AND t.total > 0
JOIN feature fe ON fe.name = x.file
WHERE x.mass > 0;
