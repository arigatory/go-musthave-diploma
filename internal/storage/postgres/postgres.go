// Package postgres implements the Gophermart storage on top of PostgreSQL.
//
// All tables live in the "gophermart" schema. Amounts of points are stored
// as BIGINT hundredths (see model.Amount).
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/arigatory/go-musthave-diploma/internal/model"
)

// Storage is a PostgreSQL-backed repository of users, orders and withdrawals.
type Storage struct {
	pool *pgxpool.Pool
}

// New connects to the database at dsn, applies migrations and returns a Storage.
func New(ctx context.Context, dsn string) (*Storage, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := Migrate(pool); err != nil {
		pool.Close()
		return nil, err
	}
	return &Storage{pool: pool}, nil
}

// Close closes all database connections.
func (s *Storage) Close() {
	s.pool.Close()
}

// Ping checks the database connection.
func (s *Storage) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// CreateUser inserts a new user and returns its ID.
// It returns model.ErrLoginTaken if the login already exists.
func (s *Storage) CreateUser(ctx context.Context, login, passwordHash string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx,
		`INSERT INTO gophermart.users (login, password_hash) VALUES ($1, $2) RETURNING id`,
		login, passwordHash,
	).Scan(&id)
	if isUniqueViolation(err) {
		return 0, model.ErrLoginTaken
	}
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	return id, nil
}

// UserByLogin returns the user with the given login or model.ErrUserNotFound.
func (s *Storage) UserByLogin(ctx context.Context, login string) (model.User, error) {
	u := model.User{Login: login}
	err := s.pool.QueryRow(ctx,
		`SELECT id, password_hash FROM gophermart.users WHERE login = $1`, login,
	).Scan(&u.ID, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.User{}, model.ErrUserNotFound
	}
	if err != nil {
		return model.User{}, fmt.Errorf("select user: %w", err)
	}
	return u, nil
}

// addOrderQuery inserts an order and returns its owner in one round trip.
// If the number is new, the row comes from the INSERT with inserted = true.
// Otherwise ON CONFLICT DO NOTHING inserts nothing and the second branch
// returns the existing owner with inserted = false; NOT EXISTS guarantees
// exactly one row.
const addOrderQuery = `
WITH ins AS (
	INSERT INTO gophermart.orders (number, user_id, status) VALUES ($1, $2, $3)
	ON CONFLICT (number) DO NOTHING
	RETURNING user_id
)
SELECT user_id, true FROM ins
UNION ALL
SELECT user_id, false FROM gophermart.orders
WHERE number = $1 AND NOT EXISTS (SELECT 1 FROM ins)`

// AddOrder registers a new order for the user with status NEW.
// It returns model.ErrOrderAlreadyUploaded if the user has already uploaded
// the order and model.ErrOrderOwnedByAnother if it belongs to another user.
func (s *Storage) AddOrder(ctx context.Context, userID int64, number string) error {
	var (
		owner    int64
		inserted bool
	)
	scan := func() error {
		return s.pool.QueryRow(ctx, addOrderQuery, number, userID, model.StatusNew).Scan(&owner, &inserted)
	}
	err := scan()
	if errors.Is(err, pgx.ErrNoRows) {
		// A concurrent transaction inserted the same number after this
		// statement took its snapshot: ON CONFLICT waited for it, but the
		// SELECT branch cannot see the row yet. It is committed now, so
		// a retry finds the owner.
		err = scan()
	}
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	switch {
	case inserted:
		return nil
	case owner == userID:
		return model.ErrOrderAlreadyUploaded
	default:
		return model.ErrOrderOwnedByAnother
	}
}

// ListOrders returns the user's orders sorted from newest to oldest.
func (s *Storage) ListOrders(ctx context.Context, userID int64) ([]model.Order, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT number, status, accrual, uploaded_at FROM gophermart.orders
		 WHERE user_id = $1 ORDER BY uploaded_at DESC, number`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("select orders: %w", err)
	}
	orders, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Order, error) {
		o := model.Order{UserID: userID}
		var accrual *int64
		if err := row.Scan(&o.Number, &o.Status, &accrual, &o.UploadedAt); err != nil {
			return o, err
		}
		if accrual != nil {
			a := model.Amount(*accrual)
			o.Accrual = &a
		}
		return o, nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan orders: %w", err)
	}
	return orders, nil
}

// Balance returns the user's current balance and total withdrawn amount.
func (s *Storage) Balance(ctx context.Context, userID int64) (model.Balance, error) {
	var b model.Balance
	err := s.pool.QueryRow(ctx,
		`SELECT balance, withdrawn FROM gophermart.users WHERE id = $1`, userID,
	).Scan(&b.Current, &b.Withdrawn)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, model.ErrUserNotFound
	}
	if err != nil {
		return b, fmt.Errorf("select balance: %w", err)
	}
	return b, nil
}

// Withdraw atomically deducts sum from the user's balance and records the
// withdrawal. It returns model.ErrInsufficientFunds if the balance is too low
// and model.ErrWithdrawalExists if the order has already been paid with points.
func (s *Storage) Withdraw(ctx context.Context, userID int64, order string, sum model.Amount) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var balance model.Amount
		err := tx.QueryRow(ctx,
			`SELECT balance FROM gophermart.users WHERE id = $1 FOR UPDATE`, userID,
		).Scan(&balance)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("lock user: %w", err)
		}
		if balance < sum {
			return model.ErrInsufficientFunds
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO gophermart.withdrawals (user_id, order_number, sum) VALUES ($1, $2, $3)`,
			userID, order, sum,
		)
		if isUniqueViolation(err) {
			return model.ErrWithdrawalExists
		}
		if err != nil {
			return fmt.Errorf("insert withdrawal: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE gophermart.users SET balance = balance - $2, withdrawn = withdrawn + $2 WHERE id = $1`,
			userID, sum,
		); err != nil {
			return fmt.Errorf("update balance: %w", err)
		}
		return nil
	})
}

// ListWithdrawals returns the user's withdrawals sorted from newest to oldest.
func (s *Storage) ListWithdrawals(ctx context.Context, userID int64) ([]model.Withdrawal, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT order_number, sum, processed_at FROM gophermart.withdrawals
		 WHERE user_id = $1 ORDER BY processed_at DESC, id DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("select withdrawals: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Withdrawal, error) {
		var w model.Withdrawal
		err := row.Scan(&w.Order, &w.Sum, &w.ProcessedAt)
		return w, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan withdrawals: %w", err)
	}
	return list, nil
}

// PendingOrders returns up to limit orders with a non-final status,
// the least recently checked first.
func (s *Storage) PendingOrders(ctx context.Context, limit int) ([]model.Order, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT number, user_id, status, uploaded_at FROM gophermart.orders
		 WHERE status IN ($1, $2)
		 ORDER BY checked_at NULLS FIRST, uploaded_at
		 LIMIT $3`,
		model.StatusNew, model.StatusProcessing, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("select pending orders: %w", err)
	}
	orders, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (model.Order, error) {
		var o model.Order
		err := row.Scan(&o.Number, &o.UserID, &o.Status, &o.UploadedAt)
		return o, err
	})
	if err != nil {
		return nil, fmt.Errorf("scan pending orders: %w", err)
	}
	return orders, nil
}

// UpdateOrderAccrual stores the result of an accrual check. If the order is
// already in a final status nothing is changed. When the new status is
// PROCESSED the accrual is credited to the owner's balance in the same
// transaction, so points can never be credited twice.
func (s *Storage) UpdateOrderAccrual(ctx context.Context, number string, status model.OrderStatus, accrual *model.Amount) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var userID int64
		err := tx.QueryRow(ctx,
			`UPDATE gophermart.orders SET status = $2, accrual = $3, checked_at = now()
			 WHERE number = $1 AND status IN ($4, $5)
			 RETURNING user_id`,
			number, status, accrual, model.StatusNew, model.StatusProcessing,
		).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("update order: %w", err)
		}
		if status != model.StatusProcessed || accrual == nil || *accrual <= 0 {
			return nil
		}
		if _, err := tx.Exec(ctx,
			`UPDATE gophermart.users SET balance = balance + $2 WHERE id = $1`, userID, *accrual,
		); err != nil {
			return fmt.Errorf("credit accrual: %w", err)
		}
		return nil
	})
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation
}
