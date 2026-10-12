package schemaassist

import (
	"math/big"
	"math/bits"
)

func (c *constraintChecker) checkMultipleOf(n map[string]any, value any, path string, inspectValue bool) {
	raw, exists := n["multipleOf"]
	if !exists {
		return
	}
	divisor, valid := c.bound(raw, false)
	if !valid {
		return
	}
	// Metadata is inspected even if the value is absent, nullable or of the
	// wrong type. A malformed divisor must not disappear behind a type hint.
	if divisor.sign <= 0 {
		c.report.Partial = true
		return
	}
	if !inspectValue || !number(value) {
		return
	}
	instance, valid := c.bound(value, false)
	if !valid {
		return
	}
	matched, complete := c.multipleOf(instance, divisor)
	if !complete {
		c.report.Partial = true
		return
	}
	if !matched {
		c.add(path, "multipleOf", "Value is not an exact multiple of the advertised divisor.")
	}
}

// Both coefficients are normalized, bounded decimal integers with no trailing
// zeros. For nonzero a, a/b is an integer exactly when
//
//	a.digits * 10^(a.power-b.power) == 0 (mod b.digits).
//
// If the exponent difference is negative, integer division would require
// a.digits to contain a trailing zero, which normalization excludes. Otherwise
// modular exponentiation bounds the remainders by the divisor and temporary
// products by coefficient length; it never constructs 10^difference. The
// instance sign does not affect divisibility.
func (c *constraintChecker) multipleOf(instance, divisor decimal) (matched, complete bool) {
	if instance.sign == 0 {
		return true, true
	}
	difference := instance.power - divisor.power
	if difference < 0 {
		return false, true
	}
	// Each coefficient has at most 512 decimal digits. The powers originate
	// from signed 32-bit exponents plus at most 512 normalization positions,
	// so their nonnegative difference needs at most 33 bits. Charge before
	// allocating big integers, conservatively weighting 64-digit blocks and
	// modular multiply/square steps. This is an inspection work allowance,
	// not a promise that a big.Int primitive is preemptible by context.
	blocks := (max(len(instance.digits), len(divisor.digits)) + 63) / 64
	work := blocks * blocks * (2*bits.Len64(uint64(difference)) + 4)
	if !c.chargeWork(work, 0) {
		return false, false
	}
	a, aOK := new(big.Int).SetString(instance.digits, 10)
	b, bOK := new(big.Int).SetString(divisor.digits, 10)
	if !aOK || !bOK || b.Sign() <= 0 {
		return false, false
	}
	remainder := new(big.Int).Mod(a, b)
	if difference != 0 && remainder.Sign() != 0 {
		power := new(big.Int).Exp(big.NewInt(10), big.NewInt(difference), b)
		remainder.Mul(remainder, power)
		remainder.Mod(remainder, b)
	}
	if c.ctx.Err() != nil {
		return false, false
	}
	return remainder.Sign() == 0, true
}
