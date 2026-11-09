package sdktest_test

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/openbekci/openbekci/server/internal/problem"
)

type pgxTx = pgx.Tx

func asProblem(err error, target **problem.Error) bool { return errors.As(err, target) }
