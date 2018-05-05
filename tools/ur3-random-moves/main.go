// u3-random-moves generates a gcode file with random moves.
// The expected use is to move the UR3 in the front of a radar + camera
// to collect training data for Radar GAN.
package main

import (
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
)

const (
	xMin     = 270
	xMax     = 320
	yMin     = -80
	yMax     = 70
	zMin     = 130
	zMax     = 270
	rollMin  = math.Pi - 0.3
	rollMax  = math.Pi + 0.3
	pitchMin = -0.3
	pitchMax = 0.3
	yawMin   = math.Pi/2 - math.Pi/2
	yawMax   = math.Pi/2 + math.Pi/2
)

var (
	steps = flag.Int("steps", 20000, "Number of steps")
)

// Matrix multiply for 3x3 matrices.
func matmul3(a, b [3][3]float64) (res [3][3]float64) {
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				res[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return
}

func rpy2RotVec(roll, pitch, yaw float64) ([3]float64, error) {
	//log.Printf("roll=%v, pitch=%v, yaw=%v", roll, pitch, yaw)
	rollM := [3][3]float64{
		{1, 0, 0},
		{0, math.Cos(roll), -math.Sin(roll)},
		{0, math.Sin(roll), math.Cos(roll)},
	}
	pitchM := [3][3]float64{
		{math.Cos(pitch), 0, math.Sin(pitch)},
		{0, 1, 0},
		{-math.Sin(pitch), 0, math.Cos(pitch)},
	}
	yawM := [3][3]float64{
		{math.Cos(yaw), -math.Sin(yaw), 0},
		{math.Sin(yaw), math.Cos(yaw), 0},
		{0, 0, 1},
	}
	rotM := matmul3(matmul3(yawM, pitchM), rollM)
	//log.Printf("rotM: %v", rotM)
	theta := math.Acos(((rotM[0][0] + rotM[1][1] + rotM[2][2]) - 1) / 2)
	var denom = 2 * math.Sin(theta)
	//log.Printf("theta: %v, denom: %v", theta, denom)
	if math.Abs(denom) < 1E-3 {
		return [3]float64{0, 0, 0}, fmt.Errorf("anomaly point (denom is close to 0), can't move")
	}
	multi := 1.0 / denom
	return [3]float64{
		multi * (rotM[2][1] - rotM[1][2]) * theta,
		multi * (rotM[0][2] - rotM[2][0]) * theta,
		multi * (rotM[1][0] - rotM[0][1]) * theta,
	}, nil
}

func ur3X(x float64) float64 {
	return -0.280 - (x-200)/1000
}

func ur3Y(y float64) float64 {
	return -0.112468 - y/1000
}

func ur3Z(z float64) float64 {
	return 0.073 + (z-90)/1000
}

func coord2UR3(x, y, z, roll, pitch, yaw float64) (string, error) {
	rotvec, err := rpy2RotVec(roll, pitch, yaw)
	if err != nil {
		return "", fmt.Errorf("can't get rotation vector from (roll=%v, pitch=%v, yaw=%v): %v", roll, pitch, yaw, err)
	}
	xx := ur3X(x)
	yy := ur3Y(y)
	zz := ur3Z(z)
	rx := rotvec[0]
	ry := rotvec[1]
	rz := rotvec[2]
	return fmt.Sprintf("movej(get_inverse_kin(p[%.6f, %.6f, %.6f, %.6f, %.6f, %.6f]), a=0.1, v=0.01)",
		xx, yy, zz, rx, ry, rz), nil
}

func randInRange(from, to float64) float64 {
	return from + rand.Float64()*(to-from)
}

func randPoint() (x, y, z, roll, pitch, yaw float64) {
	x = randInRange(xMin, xMax)
	y = randInRange(yMin, yMax)
	z = randInRange(zMin, zMax)
	roll = randInRange(rollMin, rollMax)
	pitch = randInRange(pitchMin, pitchMax)
	yaw = randInRange(yawMin, yawMax)
	return
}

func goodRandPoint() (x, y, z, roll, pitch, yaw float64) {
	for {
		x, y, z, roll, pitch, yaw = randPoint()
		if _, err := coord2UR3(x, y, z, roll, pitch, yaw); err == nil {
			return
		}
	}
}

func main() {
	flag.Parse()

	fmt.Println("; Home it first")
	fmt.Println("movej(get_inverse_kin(p[-0.32, -0.112468, 0.22599999999999998, 2.2193304157225078, 2.2215519673201243, 0.0000011102208116102493]), a=0.4, v=0.3)")
	fmt.Println("; wait for idle")
	fmt.Println("M7823")

	for i := 0; i < *steps; i++ {
		x, y, z, roll, pitch, yaw := goodRandPoint()
		cmd, err := coord2UR3(x, y, z, roll, pitch, yaw)
		if err != nil {
			log.Fatalf("coord2UR3: %v", err)
		}
		fmt.Printf("; Step %d/%d\n", i, *steps)
		fmt.Println(cmd)
		fmt.Printf("; wait for idle\n")
		fmt.Printf("M7823\n\n")
	}
}
